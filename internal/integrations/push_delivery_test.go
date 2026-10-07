package integrations

import (
	"strings"
	"testing"
)

func TestPushEndpointAndTruncation(t *testing.T) {
	for _, s := range []string{"http://fcm.googleapis.com/x", "https://fcm.googleapis.com:444/x", "https://fcm.googleapis.com.attacker.test/x", "https://localhost/x", "https://fcm.googleapis.com/a b"} {
		if _, err := PushEndpoint(s); err == nil {
			t.Errorf("allowed %q", s)
		}
	}
	for _, s := range []string{"https://fcm.googleapis.com/x", "https://a.notify.windows.com:443/x"} {
		if _, err := PushEndpoint(s); err != nil {
			t.Errorf("rejected %q", s)
		}
	}
	for _, c := range []struct {
		in, out string
		n       int
	}{{"short", "short", 5}, {"longer", "lo…", 5}, {"😀😀", "😀…", 7}, {strings.Repeat("\x01", 10), "\x01\x01…", 16}, {`"""`, `"…`, 5}} {
		if got := TruncatePush(c.in, c.n); got != c.out {
			t.Errorf("%q => %q, want %q", c.in, got, c.out)
		}
	}
}
