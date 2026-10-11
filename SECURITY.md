# Security policy

## Reporting a vulnerability

Please do not open a public issue for a security problem.

Report it privately through GitHub's private vulnerability reporting:

1. Go to the [Security tab](https://github.com/dev-dami/deck/security) of the repository.
2. Choose **Report a vulnerability** to open a draft Security Advisory.
3. Describe the problem, the steps to reproduce it, and the version of `deck` you tested (`deck version`).

The report is visible only to you and the repository maintainer, [@dev-dami](https://github.com/dev-dami). We will acknowledge it as soon as we can and keep you updated while we work on a fix. When a fix ships we will publish the advisory and credit you, unless you ask us not to.

Please give us a reasonable chance to fix the problem before you disclose it publicly.

## Scope

Deck is a terminal app that runs the user's own `claude` and `codex` CLIs. The areas we care most about are:

- **The approval broker.** The loopback TCP listener on `127.0.0.1`, and the token that authenticates the MCP helper to it. Anything that lets a connection other than Deck's own helper approve a request, or that lets one thread approve for another.
- **The MCP helper (`deck mcp-approve`).** Its stdio protocol, what it forwards, and what it accepts from the agent. It offers every agent `search` and `execute` (and Claude `approve`); every op is read-only except `thread.send`, and `skills.read` must stay inside a skill's folder.
- **Process spawning.** How Deck starts the `claude` and `codex` binaries, including argument handling, environment passing and the working directory.
- **Credentials.** Anything that could read, copy, log, print or transmit credential files, OAuth tokens, or API keys, whether from the user's CLI login or from the environment. Deck is designed never to touch them. The guard test in `internal/guard` checks part of that.
- **Agent self-approval.** Anything that lets an agent approve its own tool calls or file changes, or that bypasses the user's approval inbox.
- **Output and storage.** Thread files under `~/.deck/threads` and the config file, where a crafted agent message could cause unintended writes or code execution in the terminal.

Problems in the `claude` or `codex` CLIs themselves, in the operating system, or in your terminal emulator should be reported to their own maintainers. If you are unsure which project owns a problem, report it here and we will point you in the right direction.

Issues that only affect the look of the UI, or that need the user to run a command they were clearly warned about, are normally handled as ordinary bugs.

## Supported versions

Only the latest release of Deck receives security fixes. Please check that the problem still happens on the latest release, or on `main`, before you report it.

| version | supported |
| --- | --- |
| latest release | yes |
| older releases | no, please upgrade |

## Keeping secrets out of reports

When you report a problem, do not include your credential files, tokens, or account identifiers. `deck doctor` never prints account information, but check its output before you paste it anywhere. Remove usernames, hostnames and paths from any logs or recordings you share.
