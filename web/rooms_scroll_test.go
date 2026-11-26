package web

import (
	"os"
	"testing"
)

func TestRoomsScrollContainment(t *testing.T) {
	data, err := os.ReadFile("app/rooms.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(data)
	for selector, properties := range map[string]map[string]string{
		".shell-main>main:has(#roomsView.active)": {
			"display":        "flex",
			"flex-direction": "column",
			"min-height":     "0",
			"overflow":       "hidden",
		},
		"#roomsView.room-view.active": {
			"flex":       "1 1 auto",
			"height":     "100%",
			"min-height": "0",
		},
	} {
		body := sidebarCSSBlock(t, css, selector)
		for property, want := range properties {
			if got := sidebarCSSValue(body, property); got != want {
				t.Errorf("%s { %s } = %q, want %q", selector, property, got, want)
			}
		}
	}
}
