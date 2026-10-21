package tgbot

import "testing"

func TestConfigValidate(t *testing.T) {
	c := Default("ru")
	c.Buttons = append(c.Buttons, MenuButton{Action: "url", Label: "Канал", URL: "https://t.me/mikan", On: true},
		MenuButton{Action: "page", Label: "Правила", Text: "Не делитесь ссылкой", On: true})
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Buttons[6].ID == "" || c.Buttons[7].ID == "" || c.Buttons[6].ID == c.Buttons[7].ID {
		t.Fatalf("custom buttons get ids: %+v", c.Buttons[6:])
	}
	for name, bad := range map[string]MenuButton{
		"javascript link": {Action: "url", Label: "x", URL: "javascript:alert(1)"},
		"http link":       {Action: "url", Label: "x", URL: "http://example.com"},
		"empty page":      {Action: "page", Label: "x", Text: " "},
		"empty label":     {Action: "page", Label: " ", Text: "x"},
		"twice sub":       {Action: "sub", Label: "x"},
		"unknown action":  {Action: "pay", Label: "x"},
	} {
		c := Default("ru")
		c.Buttons = append(c.Buttons, bad)
		if err := c.Validate(); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}
