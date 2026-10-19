// Package vsh is the virtual bash sandbox for ops contracts
// (docs/peers-design.md §4). It runs a small shell dialect on the
// mvdan.cc/sh interpreter with an allow-list: a command runs only if it is a
// pure-Go builtin or a shim whose argv matches an allowed pattern.
//
// Dialect. Run parses the script first and refuses all of it, before
// anything runs, when it contains something outside the dialect. Allowed:
// simple commands, pipes (| and |&), && and ||, ; and newlines, ! negation,
// if/then/elif/else, { ...; } groups and ( ... ) subshells, single and double
// quotes, $'...' strings, globs, and the redirections > >> < &> &>> and >&1
// or >&2 on file descriptors 0, 1 and 2. Refused: loops (while, until, for,
// select), case, functions, aliases, variables and assignments ($x, x=1,
// export, declare, local, readonly), command substitution ($(...) and
// backticks), arithmetic, process substitution, here-docs and here-strings,
// background jobs (&), coproc, time, [[ ]], unquoted braces (brace
// expansion, so quote a word that contains { or }), and any command name that
// is not a plain word naming an allowed builtin or shim (eval, source, .,
// exec, cd, command, builtin, env, \podman, /usr/bin/podman ...). Loops are
// refused so that a script without them always ends. Sandbox.Timeout still
// bounds slow commands. Without loops and variables, no expansion can grow
// past the script length, except globs, which may read at most 256
// directories in one run. A script is at most 64 KiB. printf refuses -v and
// a width or precision of * or of five or more digits.
//
// Builtins: echo, printf, true, false, :, test, [ (from the interpreter),
// and cat, head, tail, grep, sort, uniq, wc, cut, tr and jq (in Go, in this
// package and internal/shell). They read files only inside Sandbox.Dir and
// read at most 64 MiB from one input. grep uses Go regexp syntax. jq sees an
// empty environment.
//
// Files. Redirections, globs, test and the builtins see only Sandbox.Dir,
// through an os.Root: relative paths that stay inside it after cleaning and
// symlink resolution, or absolute paths under it. Absolute paths elsewhere,
// .. out of the dir and symlinks that point out of it are refused. /dev/null
// is allowed and is empty. Writes count against Sandbox.QuotaBytes, together
// with the files already in the dir, and truncating a file gives its bytes
// back. A write past the quota fails, every later write in the run fails
// too, and stderr gets "vsh: scratch quota exceeded". Files that a shim
// writes itself do not count: give shims read-only verbs only.
//
// Shims. A Shim runs Binary directly with os/exec: no shell, an environment
// of only PATH=/usr/bin:/bin and LANG=C, no stdin, cwd Sandbox.Dir, in its own
// process group (Unix) that is killed at the timeout. Only the words after
// Name are matched against Allow. Binaries and names that are package
// runners, fetchers, shells, interpreters, sudo or su are never grantable,
// and neither are destructive podman or docker verbs: Run returns an error
// when a Shim names one.
//
// Pattern syntax. A Pattern is a list of elements that must match the argv
// (after Name) word by word, from start to end:
//
//	literal       the word must be equal, for example "ps" or "--type=service"
//	{target}      one of Sandbox.Targets
//	{n}           a decimal number from 0 to 100000, digits only
//	{format}      1 to 256 characters of letters, digits, space and {}._:,|=-/%"()\
//	{time}        1 to 40 characters of letters, digits and :+-.
//	[w1 w2 ...]   an optional group: the words w1 w2 ... all appear, or none
//
// Optional groups contain literals and placeholders only and are not nested.
// A flag with a value is two words ("--format" "{format}"). The "--flag=X"
// form matches only as a literal.
//
// Results. A refusal (dialect, command, pattern) gives one line on stderr
// that starts with "vsh: " and says why, and exit 126. A dialect or command
// refusal stops the script. A shim pattern refusal fails that command only.
// A timeout gives exit 124. Run returns an error only for a bad Sandbox
// (including a shim that is never grantable), when ctx ends, or for an
// interpreter failure.
package vsh
