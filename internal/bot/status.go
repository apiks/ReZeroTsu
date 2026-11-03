package bot

import "github.com/bwmarrin/discordgo"

// customStatus builds the presence update that shows msg as a custom status (activity type 4), which Discord
// renders without a "Playing" prefix. Custom activities carry their text in State; Name is a fixed label.
func customStatus(msg string) discordgo.UpdateStatusData {
	return discordgo.UpdateStatusData{
		Status: "online",
		Activities: []*discordgo.Activity{{
			Name:  "Custom Status",
			Type:  discordgo.ActivityTypeCustom,
			State: msg,
		}},
	}
}
