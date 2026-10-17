package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// userList is what GET /users answers, to the fields the organising looks at.
type userList struct {
	Items []struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Source   string `json:"source"`
		FolderID *int64 `json:"folder_id"`
		Hidden   bool   `json:"hidden"`
	} `json:"items"`
	Total  int `json:"total"`
	Counts struct {
		All    int `json:"all"`
		Active int `json:"active"`
	} `json:"counts"`
	FolderCounts map[string]int `json:"folder_counts"`
	SourceCounts map[string]int `json:"source_counts"`
	UsersTotal   int            `json:"users_total"`
	HiddenTotal  int            `json:"hidden_total"`
}

func (l userList) names() []string {
	var out []string
	for _, u := range l.Items {
		out = append(out, u.Name)
	}
	slices.Sort(out)
	return out
}

// Folders sort the list and hiding takes a row off it; neither changes what a user may do
// or what the panel counts. Filters combine, the counts follow them, an API key's scope
// is the endpoint's own, and deleting a folder deletes nobody.
func TestFoldersAndHiddenUsers(t *testing.T) {
	k := newKeyHarness(t)
	tariffs, _ := k.st.Q.ListTariffs(t.Context())
	var users = map[string]int64{}
	for _, name := range []string{"anna", "boris", "clara", "dmitry"} {
		resp, body := k.do(http.MethodPost, k.api+"/users", map[string]any{"name": name, "tariff_id": tariffs[1].ID}, k.csrf)
		var u struct {
			ID     int64  `json:"id"`
			Source string `json:"source"`
			Hidden bool   `json:"hidden"`
		}
		if resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &u) != nil || u.Source != "admin" || u.Hidden {
			t.Fatalf("create %s: %d %s", name, resp.StatusCode, body)
		}
		users[name] = u.ID
	}
	// Two of them bought in the bot: the source is a stored fact, set on its own.
	for _, name := range []string{"clara", "dmitry"} {
		if _, err := k.st.DB.ExecContext(t.Context(), "UPDATE users SET source = 'bot' WHERE id = $1", users[name]); err != nil {
			t.Fatal(err)
		}
	}
	list := func(query url.Values) userList {
		t.Helper()
		resp, body := k.do(http.MethodGet, k.api+"/users?"+query.Encode(), nil, nil)
		var l userList
		if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &l) != nil {
			t.Fatalf("list %s: %d %s", query.Encode(), resp.StatusCode, body)
		}
		return l
	}

	// Folders: the same checks as the other endpoints (a read key reads, a full key writes).
	if resp, _ := k.asKey(k.read, http.MethodPost, "/folders", map[string]any{"name": "Nope"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a read key made a folder: %d", resp.StatusCode)
	}
	mk := func(body map[string]any) (int64, int, string) {
		resp, out := k.asKey(k.full, http.MethodPost, "/folders", body)
		var f struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(out, &f)
		return f.ID, resp.StatusCode, string(out)
	}
	family, code, out := mk(map[string]any{"name": " Family ", "emoji": "🏠", "color": "blue"})
	if code != http.StatusCreated || !strings.Contains(out, `"name":"Family"`) || !strings.Contains(out, `"color":"blue"`) {
		t.Fatalf("make a folder: %d %s", code, out)
	}
	friends, code, _ := mk(map[string]any{"name": "Friends"})
	if code != http.StatusCreated {
		t.Fatalf("a folder without a colour: %d", code)
	}
	if _, code, out := mk(map[string]any{"name": "FAMILY"}); code != http.StatusConflict || !strings.Contains(out, "folder_name_taken") {
		t.Fatalf("the same name in other case: %d %s", code, out)
	}
	if _, code, _ := mk(map[string]any{"name": "Pink", "color": "#ff0000"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("a colour outside the palette: %d", code)
	}
	if _, code, _ := mk(map[string]any{"name": "   "}); code != http.StatusUnprocessableEntity {
		t.Fatalf("a blank name: %d", code)
	}
	if resp, body := k.asKey(k.read, http.MethodGet, "/folders", nil); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"users":0`) {
		t.Fatalf("a read key lists folders: %d %s", resp.StatusCode, body)
	}

	// One user into a folder through the card, several through the bulk action.
	if resp, body := k.do(http.MethodPatch, k.api+"/users/"+idOf(users["anna"]), map[string]any{"folder_id": family}, k.csrf); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"folder_id":`+idOf(family)) {
		t.Fatalf("move one: %d %s", resp.StatusCode, body)
	}
	if resp, body := k.do(http.MethodPatch, k.api+"/users/"+idOf(users["anna"]), map[string]any{"folder_id": 99999}, k.csrf); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "folder_not_found") {
		t.Fatalf("a folder that is not there: %d %s", resp.StatusCode, body)
	}
	bulk := func(action string, ids []int64, extra map[string]any) (int, string) {
		body := map[string]any{"ids": ids, "action": action}
		for key, v := range extra {
			body[key] = v
		}
		resp, out := k.do(http.MethodPost, k.api+"/users/bulk", body, k.csrf)
		return resp.StatusCode, string(out)
	}
	if code, out := bulk("move", []int64{users["boris"], users["clara"]}, map[string]any{"folder_id": 99999}); code != http.StatusUnprocessableEntity || !strings.Contains(out, "folder_not_found") {
		t.Fatalf("bulk move to a missing folder: %d %s", code, out)
	}
	if l := list(url.Values{"folder": {"none"}}); l.Total != 3 {
		t.Fatalf("a refused move moved users: %d without a folder", l.Total)
	}
	if code, out := bulk("move", []int64{users["boris"], users["clara"]}, map[string]any{"folder_id": friends}); code != http.StatusOK || !strings.Contains(out, `"affected":2`) {
		t.Fatalf("bulk move: %d %s", code, out)
	}

	// Filters combine; every count leaves out its own part.
	l := list(url.Values{})
	if l.Total != 4 || l.FolderCounts[idOf(family)] != 1 || l.FolderCounts[idOf(friends)] != 2 || l.FolderCounts["none"] != 1 || l.SourceCounts["admin"] != 2 || l.SourceCounts["bot"] != 2 {
		t.Fatalf("all users: %+v", l)
	}
	if l := list(url.Values{"folder": {idOf(friends)}}); !slices.Equal(l.names(), []string{"boris", "clara"}) || l.FolderCounts[idOf(friends)] != 2 || l.SourceCounts["bot"] != 1 {
		t.Fatalf("a folder: %+v", l)
	}
	if l := list(url.Values{"folder": {idOf(friends)}, "source": {"bot"}}); !slices.Equal(l.names(), []string{"clara"}) || l.Counts.All != 1 {
		t.Fatalf("a folder and a source: %+v", l)
	}
	if l := list(url.Values{"folder": {idOf(friends)}, "source": {"bot"}, "q": {"bor"}}); l.Total != 0 || l.Counts.All != 1 {
		t.Fatalf("a folder, a source and a search: %+v", l)
	}
	if l := list(url.Values{"folder": {"none"}, "source": {"bot"}}); !slices.Equal(l.names(), []string{"dmitry"}) {
		t.Fatalf("no folder, bought: %+v", l)
	}
	if resp, _ := k.do(http.MethodGet, k.api+"/users?folder=abc", nil, nil); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a bad folder: %d", resp.StatusCode)
	}

	// Hiding: off the list, not out of the panel.
	if code, out := bulk("hide", []int64{users["boris"], users["dmitry"]}, nil); code != http.StatusOK || !strings.Contains(out, `"affected":2`) {
		t.Fatalf("bulk hide: %d %s", code, out)
	}
	if resp, body := k.do(http.MethodPatch, k.api+"/users/"+idOf(users["anna"]), map[string]any{"hidden": true}, k.csrf); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"hidden":true`) {
		t.Fatalf("hide one: %d %s", resp.StatusCode, body)
	}
	// The panel's list asks for "hide"; the API's default keeps everyone, so a script that
	// read all users before still does.
	if l := list(url.Values{"hidden": {"hide"}}); !slices.Equal(l.names(), []string{"clara"}) || l.UsersTotal != 4 || l.HiddenTotal != 3 || l.FolderCounts[idOf(friends)] != 1 {
		t.Fatalf("hidden users on the list: %+v", l)
	}
	if l := list(url.Values{}); l.Total != 4 {
		t.Fatalf("the API's default shows everyone: %d", l.Total)
	}
	if l := list(url.Values{"hidden": {"show"}}); l.Total != 4 {
		t.Fatalf("show hidden: %d", l.Total)
	}
	if l := list(url.Values{"hidden": {"only"}}); !slices.Equal(l.names(), []string{"anna", "boris", "dmitry"}) {
		t.Fatalf("only hidden: %v", l.names())
	}
	if l := list(url.Values{"q": {"boris"}, "hidden": {"hide"}}); l.Total != 0 || l.UsersTotal != 4 {
		t.Fatalf("a search does not reach hidden users unless asked: %+v", l)
	}
	// The overview counts them all; the subscription of a hidden user still opens.
	resp, body := k.do(http.MethodGet, k.api+"/stats/overview", nil, nil)
	var ov struct {
		UsersTotal  int `json:"users_total"`
		UsersActive int `json:"users_active"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &ov) != nil || ov.UsersTotal != 4 || ov.UsersActive != 4 {
		t.Fatalf("overview with hidden users: %d %s", resp.StatusCode, body)
	}
	var one struct {
		SubURL string `json:"sub_url"`
	}
	resp, body = k.do(http.MethodGet, k.api+"/users/"+idOf(users["boris"]), nil, nil)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &one) != nil || one.SubURL == "" {
		t.Fatalf("a hidden user's card: %d %s", resp.StatusCode, body)
	}
	if resp, _ := k.do(http.MethodGet, "/"+subPath+"/"+one.SubURL[strings.LastIndex(one.SubURL, "/")+1:], nil, map[string]string{"User-Agent": "Happ/3.4.1"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("a hidden user's subscription: %d", resp.StatusCode)
	}
	if code, out := bulk("unhide", []int64{users["boris"]}, nil); code != http.StatusOK || !strings.Contains(out, `"affected":1`) {
		t.Fatalf("bulk unhide: %d %s", code, out)
	}

	// Folder 0 is no folder: the user leaves theirs through the card as well.
	if resp, body := k.do(http.MethodPatch, k.api+"/users/"+idOf(users["anna"]), map[string]any{"folder_id": 0, "hidden": false}, k.csrf); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"folder_id":null`) || !strings.Contains(string(body), `"hidden":false`) {
		t.Fatalf("take out of the folder: %d %s", resp.StatusCode, body)
	}
	if l := list(url.Values{"folder": {idOf(family)}}); l.Total != 0 || l.FolderCounts[idOf(family)] != 0 {
		t.Fatalf("a folder with nobody in it: %+v", l)
	}
	if resp, _ := k.do(http.MethodPatch, k.api+"/users/"+idOf(users["anna"]), map[string]any{"folder_id": family}, k.csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("back into the folder: %d", resp.StatusCode)
	}

	// A folder of the admin's own going away: the users stay, out of folders.
	if resp, _ := k.asKey(k.read, http.MethodDelete, "/folders/"+idOf(friends), nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a read key deleted a folder: %d", resp.StatusCode)
	}
	if resp, _ := k.asKey(k.full, http.MethodDelete, "/folders/"+idOf(friends), nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete a folder: %d", resp.StatusCode)
	}
	if resp, _ := k.asKey(k.full, http.MethodDelete, "/folders/"+idOf(friends), nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete it again: %d", resp.StatusCode)
	}
	if l := list(url.Values{"hidden": {"show"}}); l.Total != 4 || l.FolderCounts["none"] != 3 || l.FolderCounts[idOf(family)] != 1 {
		t.Fatalf("after the folder is gone: %+v", l)
	}

	// The order wants every folder once.
	other, _, _ := mk(map[string]any{"name": "Other"})
	if resp, _ := k.do(http.MethodPut, k.api+"/folders/order", map[string]any{"ids": []int64{other}}, k.csrf); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an order without a folder: %d", resp.StatusCode)
	}
	if resp, _ := k.do(http.MethodPut, k.api+"/folders/order", map[string]any{"ids": []int64{other, family}}, k.csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("an order: %d", resp.StatusCode)
	}
	resp, body = k.do(http.MethodGet, k.api+"/folders", nil, nil)
	var fs []struct {
		ID int64 `json:"id"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &fs) != nil || len(fs) != 2 || fs[0].ID != other || fs[1].ID != family {
		t.Fatalf("folders in order: %s", body)
	}
}
