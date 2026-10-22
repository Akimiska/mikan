package events

// AoiVPN fork: the torrent blocker tells the subscriber why every server went quiet. The
// panel writes it when a catch bans the user (nodesync), the bot addon words and sends it.
const TorrentBanned = "aoi.torrent_banned"

// TorrentBan is the data of TorrentBanned. The envelope's user_id is the subscription, tg_id
// the Telegram account linked to it.
type TorrentBan struct {
	Until   int64  `json:"until"`   // unix seconds: when the nodes let the user in again
	Minutes int64  `json:"minutes"` // the ban's length
	Kind    string `json:"kind,omitempty"`
	Inbound string `json:"inbound,omitempty"`
}
