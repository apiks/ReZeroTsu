package animeschedule

// ShowSettings is what a schedule page, an autopost or a new subscription reads to choose and name shows: whether
// donghua is shown, the preferred track and the preferred name.
type ShowSettings struct {
	Donghua bool
	Track   PreferredTrack
	Name    PreferredName
}

// DefaultShowSettings is the show settings where a server has none, or they cannot be read: donghua shown, Sub and
// Title.
func DefaultShowSettings() ShowSettings {
	return ShowSettings{Donghua: true, Track: PreferSub, Name: PreferTitle}
}
