// Package httpx is the request/response model the Campfire handlers are written
// against. It plays the part net/http's Request, ResponseWriter, Header and Cookie
// play in other servers, but nothing here touches a socket: the front package
// fills a Request in from a gina HTTP/1.1 or HTTP/2 exchange and copies what the
// handler wrote back into the gina response. Handlers therefore cannot tell which
// protocol carried the request, and the whole application can be exercised
// in-process with the helpers in httpxtest.
package httpx
