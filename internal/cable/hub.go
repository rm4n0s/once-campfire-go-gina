// Package cable implements the Action Cable room-message transport on gina's
// WebSocket server.
//
// Every browser holds one WebSocket (ws/wss over HTTP/1.1, or an RFC 8441 stream of
// an HTTP/2 connection). The connection's callbacks run inside the connection
// isolate on whichever HTTP shard accepted it: they speak the Action Cable
// protocol (subscribe, unsubscribe, message) and keep the subscription index.
//
// Publishing never happens on those threads. Hub.Publish hands the message to the
// bus, an isolate alone on an extra shard, which re-checks every recipient's
// session and room membership against the database, builds each distinct frame
// once, and pushes the same immutable buffer to every subscriber with
// websocket.PushShared. The bus also sends the protocol's 3 second ping and drops
// connections whose session has ended.
package cable

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rm4n0s/gina"
	ghttp "github.com/rm4n0s/gina/extensions/http"
	ws "github.com/rm4n0s/gina/extensions/websocket"

	"github.com/rm4n0s/once-campfire-go-gina/internal/database"
	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx"
	"github.com/rm4n0s/once-campfire-go-gina/internal/rails"
)

const (
	subprotocol = "actioncable-v1-json"
	pingEvery   = 3 * time.Second
	maxSubs     = 64
)

// Tags of the messages the bus isolate understands.
const (
	tagPublish    = gina.TagUserBase     // data: encoded publication
	tagDisconnect = gina.TagUserBase + 1 // data: user id (8 bytes) + reconnect flag
	tagPing       = gina.TagUserBase + 2 // timer
)

type Hub struct {
	db      *database.DB
	secrets *rails.Secrets
	ep      *ws.Endpoint

	mu          sync.RWMutex
	clients     map[*client]struct{}
	subscribers map[publication][]recipient

	sys    atomic.Pointer[gina.System]
	busRef gina.Handle
	closed atomic.Bool
}

type client struct {
	user          database.User
	token         string
	peer          ws.Peer
	subscriptions map[string]subscription // guarded by Hub.mu
}

type subscription struct {
	Channel  string
	Room     int64
	Stream   string
	Present  bool
	position int
}

type publication struct {
	room   int64
	stream string
}
type recipient struct {
	client     *client
	identifier string
	room       int64
}

func destination(sub subscription) publication {
	if sub.Channel == "RoomMessagesChannel" {
		return publication{room: sub.Room}
	}
	return publication{stream: sub.Stream}
}

func New(db *database.DB, secrets *rails.Secrets) *Hub {
	h := &Hub{db: db, secrets: secrets, clients: map[*client]struct{}{}, subscribers: make(map[publication][]recipient)}
	h.ep = ws.New(ws.Config{
		Subprotocols: []string{subprotocol},
		// The web layer has already applied the application's origin policy
		// (Sec-Fetch-Site and Origin against the public origin, honouring proxies).
		CheckOrigin: func(origin, host []byte) bool { return true },
		OnOpen:      h.onOpen,
		OnMessage:   h.onMessage,
		OnClose:     h.onClose,
	})
	return h
}

// Install registers the bus isolate on a shard of its own, appended to spec. Call it
// after the HTTP servers were installed and before gina.NewSystem.
func (h *Hub) Install(spec *gina.SystemSpec, typeID gina.TypeID) {
	spec.PoolSlots = max(spec.PoolSlots, 1<<15) // room for the pushes queued in connection mailboxes
	spec.Types = append(spec.Types, gina.RegisterType(typeID, gina.TypeOptions{SlotCount: 1, MailboxCapacity: 4096},
		func(b *bus, g *gina.Ctx, _ []byte) gina.Effect {
			b.hub = h
			g.RegisterTimer(pingEvery, tagPing)
			return gina.WaitMessage()
		}, busHandler))
	spec.Shards = append(spec.Shards, gina.ShardSpec{
		Boot: []gina.SpawnSpec{{Type: typeID, Group: gina.GroupRoot, Restart: gina.RestartPermanent}},
	})
	h.busRef = gina.MakeHandle(uint8(len(spec.Shards)-1), typeID, 0, 1)
}

// Attach connects the hub to the running system; until then publications are
// dropped (nobody can be subscribed before the server is up).
func (h *Hub) Attach(sys *gina.System) { h.sys.Store(sys) }

// MailboxCapacity is the ConnMailbox the HTTP servers need for this hub.
const MailboxCapacity = ws.MailboxCapacity

// ---- the bus isolate ----

type bus struct{ hub *Hub }

func busHandler(b *bus, g *gina.Ctx, m *gina.Message) gina.Effect {
	switch m.Tag {
	case tagPublish:
		b.hub.fanOut(g, g.Data())
	case tagDisconnect:
		if data := g.Data(); len(data) == 9 {
			b.hub.dropUser(g, int64(binary.BigEndian.Uint64(data)), data[8] == 1)
		}
	case tagPing:
		b.hub.ping(g)
		g.RegisterTimer(pingEvery, tagPing)
	case gina.TagShutdown:
		return gina.Done()
	}
	return gina.WaitMessage()
}

func (h *Hub) send(tag gina.Tag, payload []byte) {
	if sys := h.sys.Load(); sys != nil && !h.closed.Load() {
		sys.SendExternal(h.busRef, tag, payload)
	}
}

// Encoding of a publication: kind (1 byte), room (8), stream length (2), stream,
// then the JSON of the message.
func encode(key publication, message []byte) []byte {
	out := make([]byte, 0, 11+len(key.stream)+len(message))
	kind := byte(0)
	if key.stream != "" {
		kind = 1
	}
	out = append(out, kind)
	out = binary.BigEndian.AppendUint64(out, uint64(key.room))
	out = binary.BigEndian.AppendUint16(out, uint16(len(key.stream)))
	out = append(out, key.stream...)
	return append(out, message...)
}

func decode(data []byte) (publication, []byte, bool) {
	if len(data) < 11 {
		return publication{}, nil, false
	}
	room := int64(binary.BigEndian.Uint64(data[1:]))
	n := int(binary.BigEndian.Uint16(data[9:]))
	if len(data) < 11+n {
		return publication{}, nil, false
	}
	return publication{room: room, stream: string(data[11 : 11+n])}, data[11+n:], true
}

// fanOut delivers one publication. It re-checks every recipient's session and
// membership: authorization is read afresh for each publication, batching the
// distinct sessions of a room into one query.
func (h *Hub) fanOut(g *gina.Ctx, data []byte) {
	key, message, ok := decode(data)
	if !ok || key == (publication{}) {
		return
	}
	var recipients []recipient
	h.mu.RLock()
	if bucket := h.subscribers[key]; len(bucket) != 0 {
		recipients = append([]recipient(nil), bucket...)
	}
	h.mu.RUnlock()
	if len(recipients) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	groups := make(map[int64]map[string]struct{})
	for _, r := range recipients {
		if groups[r.room] == nil {
			groups[r.room] = make(map[string]struct{})
		}
		groups[r.room][r.client.token] = struct{}{}
	}
	allowed := make(map[int64]map[string]int64, len(groups))
	for room, tokens := range groups {
		keys := make([]string, 0, len(tokens))
		for token := range tokens {
			keys = append(keys, token)
		}
		var err error
		if allowed[room], err = h.db.AuthorizedSessions(ctx, keys, room); err != nil {
			allowed[room] = nil
		}
	}
	frames := make(map[string]*ws.Shared)
	for _, r := range recipients {
		if allowed[r.room][r.client.token] != r.client.user.ID {
			ws.PushClose(g, r.client.peer, 1008, "unauthorized")
			continue
		}
		frame, exists := frames[r.identifier]
		if !exists {
			identifier, _ := json.Marshal(r.identifier)
			raw := make([]byte, 0, len(identifier)+len(message)+32)
			raw = append(raw, `{"identifier":`...)
			raw = append(raw, identifier...)
			raw = append(raw, `,"message":`...)
			raw = append(raw, message...)
			raw = append(raw, '}')
			var err error
			if frame, err = ws.NewShared(ws.OpText, raw); err != nil {
				return
			}
			frames[r.identifier] = frame
		}
		ws.PushShared(g, r.client.peer, frame)
	}
}

// ping sends the protocol's keep-alive to every client and closes the ones whose
// session has ended.
func (h *Hub) ping(g *gina.Ctx) {
	h.mu.RLock()
	clients := make([]*client, 0, len(h.clients))
	tokens := make([]string, 0, len(h.clients))
	seen := map[string]bool{}
	for c := range h.clients {
		clients = append(clients, c)
		if !seen[c.token] {
			seen[c.token] = true
			tokens = append(tokens, c.token)
		}
	}
	h.mu.RUnlock()
	if len(clients) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	valid, err := h.db.AuthorizedSessions(ctx, tokens, 0)
	if err != nil {
		valid = nil
	}
	frame, err := ws.NewShared(ws.OpText, []byte(fmt.Sprintf(`{"type":"ping","message":%d}`, time.Now().Unix())))
	if err != nil {
		return
	}
	for _, c := range clients {
		if err == nil && valid != nil {
			if _, ok := valid[c.token]; !ok {
				ws.PushClose(g, c.peer, 1008, "unauthorized")
				continue
			}
		}
		ws.PushShared(g, c.peer, frame)
	}
}

func (h *Hub) dropUser(g *gina.Ctx, user int64, reconnect bool) {
	h.mu.RLock()
	var peers []ws.Peer
	for c := range h.clients {
		if c.user.ID == user {
			peers = append(peers, c.peer)
		}
	}
	h.mu.RUnlock()
	if len(peers) == 0 {
		return
	}
	notice, err := ws.NewShared(ws.OpText, []byte(fmt.Sprintf(`{"type":"disconnect","reason":"remote","reconnect":%t}`, reconnect)))
	if err != nil {
		return
	}
	for _, p := range peers {
		ws.PushShared(g, p, notice)
		ws.PushClose(g, p, 1000, "")
	}
}

// ---- publishing (any goroutine) ----

func (h *Hub) Publish(ctx context.Context, room int64, markup string) {
	h.publish(publication{room: room}, markup)
}

func (h *Hub) PublishStream(ctx context.Context, name string, message any) {
	h.publish(publication{stream: name}, message)
}

func (h *Hub) publish(key publication, message any) {
	if key == (publication{}) || h.sys.Load() == nil {
		return
	}
	// Skip the encoding work when nobody is listening.
	h.mu.RLock()
	listening := len(h.subscribers[key]) != 0
	h.mu.RUnlock()
	if !listening {
		return
	}
	raw, err := json.Marshal(message)
	if err != nil {
		return
	}
	h.send(tagPublish, encode(key, raw))
}

func (h *Hub) Disconnect(user int64) { h.disconnect(user, false) }
func (h *Hub) Reconnect(user int64)  { h.disconnect(user, true) }
func (h *Hub) disconnect(user int64, reconnect bool) {
	var payload [9]byte
	binary.BigEndian.PutUint64(payload[:], uint64(user))
	if reconnect {
		payload[8] = 1
	}
	h.send(tagDisconnect, payload[:])
}

// Close stops publishing; the connections end with the gina system.
func (h *Hub) Close() { h.closed.Store(true) }

// ---- subscription index ----

// Subscription state and its routing index have one owner under h.mu. The index
// only selects candidates; session and membership checks remain publication-local.
func (h *Hub) setSubscription(c *client, identifier string, sub subscription) {
	if old, exists := c.subscriptions[identifier]; exists {
		if destination(old) == destination(sub) && old.Room == sub.Room {
			sub.position = old.position
			c.subscriptions[identifier] = sub
			return
		}
		h.unindex(old)
	}
	key := destination(sub)
	sub.position = -1
	if key != (publication{}) {
		bucket := h.subscribers[key]
		sub.position = len(bucket)
		h.subscribers[key] = append(bucket, recipient{c, identifier, sub.Room})
	}
	c.subscriptions[identifier] = sub
}

// Dense routing buckets make publication a contiguous O(recipients) copy;
// each subscription's inverse position makes removal O(1) by swapping the tail.
func (h *Hub) unindex(sub subscription) {
	key := destination(sub)
	if key == (publication{}) || sub.position < 0 {
		return
	}
	bucket := h.subscribers[key]
	last := len(bucket) - 1
	if last < 0 || sub.position > last {
		return
	}
	moved := bucket[last]
	bucket[sub.position] = moved
	bucket[last] = recipient{} // Do not retain disconnected sockets in spare capacity.
	bucket = bucket[:last]
	if sub.position != last {
		other := moved.client.subscriptions[moved.identifier]
		other.position = sub.position
		moved.client.subscriptions[moved.identifier] = other
	}
	if len(bucket) == 0 {
		delete(h.subscribers, key)
	} else {
		h.subscribers[key] = bucket
	}
}

// ---- connections ----

// Serve upgrades an authenticated request to an Action Cable WebSocket. w must
// have been produced by the front package (it exposes the gina exchange).
func (h *Hub) Serve(w httpx.ResponseWriter, r *httpx.Request, user database.User, token string) {
	exchange, ok := httpx.Underlying[interface{ Exchange() *ghttp.Context }](w)
	if !ok {
		httpx.Error(w, "WebSocket not available", 501)
		return
	}
	c := exchange.Exchange()
	conn, ok := h.ep.Upgrade(c)
	if raw, ok := httpx.Underlying[interface{ Handover() }](w); ok {
		raw.Handover() // the upgrade (or its rejection) is already on the exchange
	}
	if !ok {
		return
	}
	conn.Data = &client{user: user, token: token, subscriptions: map[string]subscription{}}
}

func (h *Hub) onOpen(conn *ws.Conn) {
	c, ok := conn.Data.(*client)
	if !ok {
		conn.Close(ws.CloseInternalError, "")
		return
	}
	if conn.Subprotocol() != subprotocol {
		conn.Close(ws.ClosePolicyViolation, "unsupported protocol")
		return
	}
	c.peer = conn.Peer()
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	conn.SendText(`{"type":"welcome"}`)
}

func (h *Hub) onClose(conn *ws.Conn, code uint16, reason []byte) {
	c, ok := conn.Data.(*client)
	if !ok {
		return
	}
	h.mu.Lock()
	delete(h.clients, c)
	subs := c.subscriptions
	for identifier := range subs {
		h.unindex(subs[identifier])
	}
	h.mu.Unlock()
	for _, sub := range subs {
		if sub.Present {
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			h.db.Presence(ctx, c.user.ID, sub.Room, "absent")
			stop()
		}
	}
}

func reply(conn *ws.Conn, kind, identifier string) {
	data, _ := json.Marshal(map[string]string{"type": kind, "identifier": identifier})
	conn.SendText(string(data))
}

func (h *Hub) onMessage(conn *ws.Conn, op ws.Opcode, data []byte) {
	c, ok := conn.Data.(*client)
	if !ok {
		return
	}
	if op != ws.OpText {
		conn.Close(ws.CloseUnsupportedData, "text commands required")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var command struct{ Command, Identifier, Data string }
	if json.Unmarshal(data, &command) != nil || len(command.Identifier) > 4096 {
		return
	}
	switch command.Command {
	case "subscribe":
		sub, valid := h.subscription(ctx, c, command.Identifier)
		h.mu.Lock()
		_, exists := c.subscriptions[command.Identifier]
		available := exists || len(c.subscriptions) < maxSubs
		h.mu.Unlock()
		if valid && available {
			if !exists && sub.Channel == "PresenceChannel" {
				if h.db.Presence(ctx, c.user.ID, sub.Room, "present") != nil {
					valid = false
				} else {
					sub.Present = true
				}
			}
			if valid {
				h.mu.Lock()
				if !exists {
					h.setSubscription(c, command.Identifier, sub)
				}
				h.mu.Unlock()
				reply(conn, "confirm_subscription", command.Identifier)
				if sub.Channel == "PresenceChannel" {
					h.PublishStream(ctx, fmt.Sprintf("user_%d_reads", c.user.ID), map[string]any{"room_id": sub.Room})
				}
				return
			}
		}
		reply(conn, "reject_subscription", command.Identifier)
	case "unsubscribe":
		h.mu.Lock()
		sub, exists := c.subscriptions[command.Identifier]
		if exists {
			h.unindex(sub)
			delete(c.subscriptions, command.Identifier)
		}
		h.mu.Unlock()
		if exists && sub.Present {
			h.db.Presence(ctx, c.user.ID, sub.Room, "absent")
		}
	case "message":
		h.mu.RLock()
		sub, exists := c.subscriptions[command.Identifier]
		h.mu.RUnlock()
		if !exists {
			return
		}
		var payload struct{ Action string }
		if json.Unmarshal([]byte(command.Data), &payload) != nil {
			return
		}
		if _, err := h.db.SessionUser(ctx, c.token); err != nil {
			conn.Close(1008, "unauthorized")
			return
		}
		if sub.Room != 0 {
			if _, err := h.db.Room(ctx, c.user.ID, sub.Room); err != nil {
				return
			}
		}
		switch sub.Channel {
		case "TypingNotificationsChannel":
			if payload.Action == "start" || payload.Action == "stop" {
				h.PublishStream(ctx, sub.Stream, map[string]any{"action": payload.Action, "user": map[string]any{"id": c.user.ID, "name": c.user.Name}})
			}
		case "PresenceChannel":
			action := payload.Action
			if action != "present" && action != "absent" && action != "refresh" {
				return
			}
			if action == "present" && sub.Present {
				action = "refresh"
			}
			if action == "absent" && !sub.Present {
				return
			}
			if h.db.Presence(ctx, c.user.ID, sub.Room, action) == nil {
				sub.Present = action != "absent"
				h.mu.Lock()
				h.setSubscription(c, command.Identifier, sub)
				h.mu.Unlock()
				if payload.Action == "present" {
					h.PublishStream(ctx, fmt.Sprintf("user_%d_reads", c.user.ID), map[string]any{"room_id": sub.Room})
				}
			}
		}
	}
}

func (h *Hub) subscription(ctx context.Context, c *client, identifier string) (subscription, bool) {
	var params struct {
		Channel string
		Signed  string          `json:"signed_stream_name"`
		Room    json.RawMessage `json:"room_id"`
	}
	if json.Unmarshal([]byte(identifier), &params) != nil {
		return subscription{}, false
	}
	sub := subscription{Channel: params.Channel}
	switch params.Channel {
	case "ApplicationCable::Channel", "HeartbeatChannel":
		return sub, true
	case "ReadRoomsChannel":
		sub.Stream = fmt.Sprintf("user_%d_reads", c.user.ID)
		return sub, true
	case "UnreadRoomsChannel":
		sub.Stream = fmt.Sprintf("user_%d_unreads", c.user.ID)
		return sub, true
	case "RoomMessagesChannel":
		name, err := h.secrets.VerifyStream(params.Signed)
		if err != nil {
			return sub, false
		}
		kind, id, err := rails.StreamRoom(name)
		if err != nil {
			return sub, false
		}
		actual, err := h.db.Room(ctx, c.user.ID, id)
		if err != nil || kind != "Room" && kind != actual.Type {
			return sub, false
		}
		sub.Room = id
		return sub, true
	case "RoomChannel", "PresenceChannel", "TypingNotificationsChannel":
		raw := string(params.Room)
		var str string
		if json.Unmarshal(params.Room, &str) == nil {
			raw = str
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return sub, false
		}
		if _, err = h.db.Room(ctx, c.user.ID, id); err != nil {
			return sub, false
		}
		sub.Room = id
		sub.Stream = fmt.Sprintf("%s:%d", params.Channel, id)
		return sub, true
	case "Turbo::StreamsChannel":
		name, err := h.secrets.VerifyStream(params.Signed)
		if err != nil {
			return sub, false
		}
		if _, suffix, ok := strings.Cut(name, ":"); ok && suffix == "messages" {
			return sub, false
		}
		sub.Stream = name
		return sub, true
	}
	return sub, false
}
