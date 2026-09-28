package nodesync

import "mikan/internal/panel/store/db"

func dbTraffic(id, down int64) db.AddUserTrafficParams {
	return db.AddUserTrafficParams{Down: down, ID: id}
}
