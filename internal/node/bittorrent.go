package node

import (
	"bytes"

	"github.com/metacubex/mihomo/component/sniffer"

	"mikan/internal/nodeapi"
)

// The torrent blocker's patterns. Each is a fixed shape BitTorrent itself defines, so
// other traffic does not match by chance: a false catch bans a paying user. Encrypted
// BitTorrent (MSE/PE) is noise to these and goes through. The core's BitTorrent sniffer
// recognises the peer wire handshake and the UDP side; the HTTP tracker is the node's.

var (
	btProtocol = []byte("\x13BitTorrent protocol") // BEP 3: the peer wire handshake
	dhtQuery   = []byte("d1:ad2:id20:")            // BEP 5: a query, keys in bencode order
	dhtReply   = []byte("d1:rd2:id20:")            // and a reply
)

// udpTrackerMagic opens a UDP tracker's connect request (BEP 15).
const udpTrackerMagic = 0x41727101980

// torrentTCP says what BitTorrent the first bytes of a TCP stream are, "" when none.
func torrentTCP(b []byte) string {
	if sniffer.SniffBitTorrentHandshake(b) == nil {
		return nodeapi.TorrentHandshake
	}
	if httpTracker(b) {
		return nodeapi.TorrentTracker
	}
	return ""
}

// httpTracker: an HTTP tracker's announce or scrape (BEP 3, BEP 48), a GET whose query
// carries the torrent's info_hash.
func httpTracker(b []byte) bool {
	if !bytes.HasPrefix(b, []byte("GET /")) {
		return false
	}
	line := b
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		line = b[:i]
	}
	if !bytes.Contains(line, []byte("info_hash=")) {
		return false
	}
	return bytes.Contains(line, []byte("/announce")) || bytes.Contains(line, []byte("/scrape")) ||
		bytes.Contains(line, []byte("peer_id="))
}

// torrentUDP says what BitTorrent a UDP packet is, "" when none: the core's sniffer knows
// the DHT, the UDP tracker's connect request and the packet that opens a uTP connection.
func torrentUDP(b []byte) string {
	switch {
	case sniffer.SniffDHT(b) == nil:
		return nodeapi.TorrentDHT
	case sniffer.SniffUDPTracker(b) == nil:
		return nodeapi.TorrentTracker
	case sniffer.SniffUTP(b) == nil:
		return nodeapi.TorrentUTP
	}
	return ""
}
