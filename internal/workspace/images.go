package workspace

// ImageTaker is a workspace that decides for itself whether a message may
// carry images, instead of the configured model's capabilities. Deck's
// workspace takes them: its agent CLIs read images, and the configured
// Crush model it would otherwise check is never set in Deck.
type ImageTaker interface {
	AcceptsImages() bool
}

var _ ImageTaker = (*DeckWorkspace)(nil)
