package node

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDNS answers every query but every fourth: 25% loss.
func fakeDNS(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for i := 1; ; i++ {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if i%4 == 0 {
				continue
			}
			_, _ = pc.WriteTo(buf[:n], addr)
		}
	}()
	return pc.LocalAddr().String()
}

func TestSpeedTest(t *testing.T) {
	var uploaded atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/down":
			if r.URL.Query().Get("bytes") == "" {
				http.Error(w, "no size", http.StatusBadRequest)
				return
			}
			chunk := make([]byte, 64<<10)
			for {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		case "/up":
			n, _ := io.Copy(io.Discard, r.Body)
			uploaded.Add(n)
		}
	}))
	defer srv.Close()
	target := speedTarget{DNS: fakeDNS(t), Down: srv.URL + "/down", Up: srv.URL + "/up", Pings: 8, Spend: 300 * time.Millisecond, Client: srv.Client()}
	now := time.Unix(1_800_000_000, 0)
	res, err := runSpeedTest(context.Background(), target, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" || res.DownBps <= 0 || res.UpBps <= 0 || uploaded.Load() == 0 {
		t.Fatalf("both ways measured: %+v (uploaded %d)", res, uploaded.Load())
	}
	if res.LossPct != 25 || res.PingMs < 0 || res.JitterMs < 0 || !res.At.Equal(now) {
		t.Fatalf("2 of 8 queries lost: %+v", res)
	}

	// One test at a time.
	speedTesting.Lock()
	if _, err := runSpeedTest(context.Background(), target, time.Now); err != ErrSpeedTestBusy {
		t.Fatalf("a second test at once: %v", err)
	}
	speedTesting.Unlock()

	// Nothing answers: the times say so, and the broken step is named.
	dead := speedTarget{DNS: "127.0.0.1:9", Down: "http://127.0.0.1:9/down", Up: "http://127.0.0.1:9/up", Pings: 2, Spend: 100 * time.Millisecond, Client: &http.Client{Timeout: time.Second}}
	res, _ = runSpeedTest(context.Background(), dead, time.Now)
	if res.PingMs != -1 || res.LossPct != 100 || !strings.HasPrefix(res.Error, "download:") {
		t.Fatalf("an unreachable target: %+v", res)
	}
}
