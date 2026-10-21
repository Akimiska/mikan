package node

import (
	"crypto/rand"
	"io"
	"net"
	"testing"
	"time"

	"github.com/metacubex/mihomo/listener/inner"
)

// A REALITY server mirrors the ClientHello to its dest one read at a time: a hello over
// the MSS comes in two writes, the second some time after the first. The dest must get
// all of it, or it never answers and the relay's client times out.
func TestInnerDialCarriesSplitWrites(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan int, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _ := io.ReadFull(c, make([]byte, 1800))
		got <- n
	}()

	conn, err := inner.HandleTcp(&Tunnel{}, ln.Addr().String(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hello := make([]byte, 1800)
	_, _ = rand.Read(hello)
	if _, err := conn.Write(hello[:1448]); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	done := make(chan error, 1)
	go func() {
		_, err := conn.Write(hello[1448:])
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second write: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the second write of the ClientHello blocks")
	}
	if n := <-got; n != len(hello) {
		t.Fatalf("dest got %d of %d bytes", n, len(hello))
	}
}
