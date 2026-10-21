package botpanel

import (
	"testing"
	"time"

	"mikan/internal/panel/domain"
)

// /start c_<code>: an ad's link makes a new account the campaign's and greets it with the
// menu as usual.
func TestStartByCampaignLink(t *testing.T) {
	e := setup(t)
	camp, err := domain.NewCampaigns(e.st, e.clock).Create(e.ctx, "VK", "vk-oct")
	if err != nil {
		t.Fatal(err)
	}
	const visitor = 777
	n := e.tg.count()
	e.say(visitor, "/start c_vk-oct")
	e.tg.wait(t, n, "sendMessage")
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := e.st.Q.GetTgChat(e.ctx, visitor)
		if err == nil && c.CampaignID.Int64 == camp.ID {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the account is not the campaign's: %+v %v", c, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
