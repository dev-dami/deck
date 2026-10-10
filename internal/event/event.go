// Package event is where usage telemetry used to be sent. Deck sends none:
// every function here does nothing, and is kept so callers need no change.
package event

func SetNonInteractive(bool)      {}
func SetContinueBySessionID(bool) {}
func SetContinueLastSession(bool) {}
func Init()                       {}
func GetID() string               { return "" }
func Alias(string)                {}
func send(string, ...any)         {}
func Error(any, ...any)           {}
func Flush()                      {}
