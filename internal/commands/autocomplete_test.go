package commands

import (
	"fmt"
	"reflect"
	"testing"
)

func ident(s string) []string { return []string{s} }

func TestMatchAnywhere(t *testing.T) {
	many := make([]string, 40)
	for i := range many {
		many[i] = fmt.Sprintf("item%02d", i)
	}
	tests := []struct {
		name  string
		items []string
		typed string
		want  []string
	}{
		{"blank keeps all in order", []string{"b", "a"}, "  ", []string{"b", "a"}},
		{"matches in the middle", []string{"Friday", "Monday"}, "day", []string{"Friday", "Monday"}},
		{"case insensitive", []string{"Grand Raffle"}, "RAF", []string{"Grand Raffle"}},
		{"start matches rank first", []string{"my raffle", "raffle two", "other raffle", "raffle one"}, "raffle", []string{"raffle two", "raffle one", "my raffle", "other raffle"}},
		{"non matches dropped", []string{"alpha", "beta"}, "zzz", nil},
		{"cap at 25", many, "item", many[:25]},
		{"cap applies after ranking", append(append([]string{}, many...), "zitem"), "zitem", []string{"zitem"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchAnywhere(tt.items, tt.typed, ident)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchAnywhereAnyText(t *testing.T) {
	type ch struct{ name, id string }
	items := []ch{{"#general", "111"}, {"#random", "222"}}
	got := MatchAnywhere(items, "22", func(c ch) []string { return []string{c.name, c.id} })
	if len(got) != 1 || got[0].id != "222" {
		t.Fatalf("got %v", got)
	}
}
