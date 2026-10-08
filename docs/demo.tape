# Deck demo recording for Charm VHS: https://github.com/charmbracelet/vhs
#
# Regenerate with:  vhs docs/demo.tape
# Needs `deckdemo` on PATH:  go install github.com/dev-dami/deck/cmd/deckdemo@latest
#
# deckdemo runs the full TUI against scripted fake agents, so recording spends
# no tokens and needs no login. The scripted duo plays out like this after the
# prompt is sent: Claude plans and asks Codex for a review, Codex answers with
# an @claude line, and about 3 seconds in Claude asks to edit a test file. That
# approval then waits for a key, so the recording is deterministic.

Require deckdemo

Output docs/demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1280
Set Height 720
Set Padding 16
Set TypingSpeed 40ms
Set Theme { "name": "deck", "black": "#0b0b0d", "red": "#e06c75", "green": "#98c379", "yellow": "#e5c07b", "blue": "#61afef", "magenta": "#c678dd", "cyan": "#56b6c2", "white": "#c9ccd3", "brightBlack": "#5c6370", "brightRed": "#e88a93", "brightGreen": "#b0d68f", "brightYellow": "#ecce93", "brightBlue": "#82c0f3", "brightMagenta": "#d89ae8", "brightCyan": "#7bc8d2", "brightWhite": "#f2f3f5", "background": "#0b0b0d", "foreground": "#d7dae0", "selection": "#2a2d36", "cursor": "#d7dae0" }

# Start the app off camera so the recording opens on the logo.
Hide
Type "deckdemo"
Enter
Sleep 500ms
Show

# The liquid logo plays its intro (about 2 seconds), then settles.
Sleep 3s

# One message addresses both agents.
Type "@claude plan the store refactor, @codex review it"
Sleep 1s
Enter

# The duo works: plan, review request, review, reply.
Sleep 5s

# Claude asks to edit a test file; the approval card is on screen.
Sleep 2s

# Open the approval inbox, take a look, then allow the edit.
Type "i"
Sleep 2s
Type "y"

# Let the edit land and the thread finish.
Sleep 5s

# Quit: the first ctrl+c arms it, the second one exits.
Ctrl+C
Sleep 300ms
Ctrl+C
Sleep 500ms
