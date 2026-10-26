import Image from "next/image";
import { Command } from "./components";

const REPO = "https://github.com/dev-dami/deck";
const RELEASES = "https://github.com/dev-dami/deck-releases/releases";
const DOCS = `${REPO}#readme`;
const INSTALL_SH = "curl -fsSL https://raw.githubusercontent.com/dev-dami/deck-releases/main/install.sh | sh";
const INSTALL_PS = 'powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/dev-dami/deck-releases/main/install.ps1 | iex"';

// Deck's mark: the app icon's amber prompt and its cursor.
function Mark({ className }: { className?: string }) {
  return (
    <svg aria-hidden viewBox="262 312 500 400" className={className}>
      <defs>
        <linearGradient id="mark-amber" x1="0" y1="360" x2="0" y2="662" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#f7bb5c" />
          <stop offset="1" stopColor="#ea972f" />
        </linearGradient>
      </defs>
      <polyline
        points="310,360 505,512 310,662"
        fill="none"
        stroke="url(#mark-amber)"
        strokeWidth="84"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <rect x="563" y="590" width="191" height="72" rx="36" fill="currentColor" />
    </svg>
  );
}

// A picture with its museum label underneath.
function Plate({
  src,
  alt,
  w,
  h,
  artist,
  title,
  date,
  className,
  priority = false,
}: {
  src: string;
  alt: string;
  w: number;
  h: number;
  artist: string;
  title: string;
  date: string;
  className?: string;
  priority?: boolean;
}) {
  return (
    <figure className={`plate ${className ?? ""}`}>
      <Image src={src} alt={alt} width={w} height={h} priority={priority} />
      <figcaption>
        {artist}, <i>{title}</i>, {date}. The Met, public domain.
      </figcaption>
    </figure>
  );
}

// One line of the typeset exchange.
function Line({ who, glyph, hue, children }: { who: string; glyph?: string; hue?: string; children: React.ReactNode }) {
  return (
    <div className="line">
      <span className="who" style={hue === undefined ? undefined : { color: hue }}>
        {glyph !== undefined && <span aria-hidden>{glyph} </span>}
        {who}
      </span>
      <p>{children}</p>
    </div>
  );
}

const AGENTS = [
  { glyph: "✻", name: "Claude Code", hue: "var(--claude)" },
  { glyph: ">_", name: "Codex", hue: "var(--ink)" },
  { glyph: "▯", name: "OpenCode", hue: "var(--opencode)" },
  { glyph: "◎", name: "Antigravity", hue: "var(--antigravity)" },
];

const PRINCIPLES = [
  ["Your CLIs, your plans.", "Deck drives the agent CLIs you already have, under your own logins. It never calls a model API and never reads a credential."],
  ["Many agents, one thread.", "Ask two at once. They hand work to each other in plain sight, start subagents, race on worktrees and review each other's changes."],
  ["A shared board.", "Agents plan on the thread's task board, claim files before they touch them, and stay inside the budgets you set."],
  ["Terminal, desktop, phone.", "The same threads in a TUI, a desktop app and a phone remote. Two Decks can link, even behind NAT, through a third they both trust."],
  ["Quiet.", "No telemetry, no language servers, no MCP servers of its own. The desktop app checks for its own updates, and nothing else phones home."],
];

export default function Home() {
  return (
    <>
      <header className="nav wrap">
        <a href={REPO} className="brand" aria-label="Deck on GitHub">
          <Mark className="brand-mark" />
          deck
        </a>
        <nav aria-label="Site">
          <a href="#thread">How it works</a>
          <a href="#terminal">Terminal</a>
          <a href="#install">Install</a>
          <a href={DOCS}>Docs</a>
          <a href={REPO} className="pill">
            GitHub <span aria-hidden>↗</span>
          </a>
        </nav>
      </header>

      <main>
        {/* The opening: the promise, and a print of a crowd crossing one bridge in the rain. */}
        <section className="hero wrap">
          <div className="hero-copy">
            <p className="eyebrow">For Claude Code, Codex, OpenCode and Antigravity</p>
            <h1>
              One deck
              <br />
              for <em>all</em> your
              <br />
              agents.
            </h1>
            <p className="lede">
              Run your agent CLIs side by side in shared threads. They hand work to each other where you can see it, and every
              reply, diff and approval lands in one place: your terminal, a desktop window, or your phone.
            </p>
            <div className="ctas">
              <a className="btn btn-ink" href={RELEASES}>
                Download Deck
              </a>
              <a className="btn btn-line" href={REPO}>
                <span aria-hidden>★</span> Star on GitHub
              </a>
            </div>
            <Command cmd={INSTALL_SH} />
          </div>
          <Plate
            className="hero-plate"
            priority
            src="/art/hiroshige-sudden-shower.webp"
            alt="A woodblock print: travellers hurry across a long wooden bridge under a sudden downpour, a raft on the river below."
            w={1148}
            h={1780}
            artist="Utagawa Hiroshige"
            title="Sudden Shower over Shin-Ōhashi Bridge and Atake"
            date="1857"
          />
        </section>

        <section className="works wrap" aria-label="Works with">
          <span className="works-label">Works with the CLIs you have</span>
          <ul>
            {AGENTS.map((a) => (
              <li key={a.name}>
                <span aria-hidden className="works-glyph" style={{ color: a.hue }}>
                  {a.glyph}
                </span>
                {a.name}
              </li>
            ))}
          </ul>
        </section>

        {/* The desktop app, over a Yosemite valley. */}
        <section className="room" id="desktop">
          <div className="wrap section-head">
            <p className="eyebrow">The desktop app</p>
            <h2>
              Every thread,
              <br />
              <em>a room.</em>
            </h2>
            <p>
              Open a project folder and its threads become rooms. Each one says what it is doing: working, needs you, two
              agents on one file, done. Diffs, the task board and a terminal sit beside the conversation, and the Graph shows
              who is doing what.
            </p>
          </div>
          <div className="room-stage">
            <Image
              className="room-backdrop"
              src="/art/watkins-yosemite.webp"
              alt=""
              width={2600}
              height={2050}
            />
            <figure className="window">
              <Image
                src="/shots/desktop-room-dark.webp"
                alt="The Deck desktop app: a thread where claude and codex answer one question, with the thread list on the left and the composer floating at the bottom."
                width={2720}
                height={1720}
              />
            </figure>
          </div>
          <p className="wrap stage-credit">
            Carleton E. Watkins, <i>Yosemite Valley from Union Point</i>, about 1872. The Met, public domain.
          </p>
        </section>

        {/* A thread, typeset as it reads. */}
        <section className="thread wrap" id="thread">
          <div className="section-head">
            <p className="eyebrow">How it works</p>
            <h2>
              Many agents,
              <br />
              <em>one thread.</em>
            </h2>
            <p>
              Name who should answer. Agents pass work to each other the same way, so the hand-off is in the thread and not
              in a log you never read.
            </p>
          </div>
          <div className="script" aria-label="An example thread">
            <Line who="you">
              <span className="mention">@claude @codex</span> why does the pairing limiter lock everyone out?
            </Line>
            <Line who="claude" glyph="✻" hue="var(--claude)">
              Every address shares one failure bucket, so twenty bad codes from one phone lock out all of them. I&rsquo;ll key
              the count by address. <span className="mention">@codex</span>, can you write the test?
            </Line>
            <Line who="codex" glyph=">_" hue="var(--ink)">
              On it: one phone fails twenty times, another still pairs. Running it against claude&rsquo;s change.
            </Line>
            <Line who="deck" hue="var(--faint)">
              <span className="aside">2 files changed · +15 −1 · tests pass</span>
            </Line>
          </div>
          <ol className="principles">
            {PRINCIPLES.map(([title, body], i) => (
              <li key={title}>
                <span className="numeral" aria-hidden>
                  {i + 1}
                </span>
                <h3>{title}</h3>
                <p>{body}</p>
              </li>
            ))}
          </ol>
        </section>

        {/* The terminal, at night, beside Jakuchū's plum tree under the moon. */}
        <section className="night" id="terminal">
          <div className="wrap night-grid">
            <Plate
              className="night-plate"
              src="/art/jakuchu-plum-moon.webp"
              alt="A hanging scroll: a white plum tree in full blossom under a pale full moon."
              w={1114}
              h={2000}
              artist="Itō Jakuchū"
              title="White Plum Blossoms and Moon"
              date="1755"
            />
            <div className="night-copy">
              <p className="eyebrow">The terminal app</p>
              <h2>
                Or never leave
                <br />
                <em>the terminal.</em>
              </h2>
              <p>
                Run <code>deck</code> in a project folder and type. <code>@</code> picks who answers, <code>#</code> adds a
                file, Tab switches plan mode, and approvals open with the command or the diff. Panes for tasks, agents and
                plan usage sit beside the chat.
              </p>
              <Image
                className="tui"
                src="/shots/tui-thread.webp"
                alt="Deck in a terminal: codex writes a test that claude handed to it, with the task list and both agents in the sidebar."
                width={2528}
                height={2082}
              />
            </div>
          </div>
        </section>

        <section className="install wrap" id="install">
          <div className="section-head">
            <p className="eyebrow">Install</p>
            <h2>
              One line,
              <br />
              <em>then sign in.</em>
            </h2>
            <p>
              The installer puts the latest release in place and checks it against its checksum. Then sign in to{" "}
              <code>claude</code>, <code>codex</code> or <code>opencode</code> yourself, as you always have.
            </p>
          </div>
          <div className="install-steps">
            <Command label="macOS and Linux" cmd={INSTALL_SH} />
            <Command label="Windows, in PowerShell" cmd={INSTALL_PS} />
            <div className="install-then">
              <Command label="Then, in a project folder" cmd="deck doctor" />
              <Command cmd="deck" />
            </div>
            <p className="install-note">
              The desktop app is in the same <a href={RELEASES}>releases</a>: a <code>.dmg</code> for macOS, an{" "}
              <code>.exe</code> for Windows, an AppImage and a <code>.deb</code> for Linux. It carries its own engine.
            </p>
          </div>
        </section>

        {/* The close: Kōrin's irises along the zigzag bridge. */}
        <section className="close">
          <Image
            className="close-art"
            src="/art/korin-irises.webp"
            alt="A pair of gold-leaf folding screens: blue irises growing along a zigzag wooden bridge."
            width={1904}
            height={874}
          />
          <div className="wrap close-copy">
            <h2>
              Bring every agent.
              <br />
              <em>Keep your own plans.</em>
            </h2>
            <div className="ctas">
              <a className="btn btn-ink" href={RELEASES}>
                Download Deck
              </a>
              <a className="btn btn-line" href={REPO}>
                <span aria-hidden>★</span> Star on GitHub
              </a>
            </div>
          </div>
        </section>
      </main>

      <footer className="foot wrap">
        <div className="foot-row">
          <span className="brand">
            <Mark className="brand-mark" />
            deck
          </span>
          <nav aria-label="Footer">
            <a href={REPO}>GitHub</a>
            <a href={RELEASES}>Releases</a>
            <a href={DOCS}>Docs</a>
          </nav>
        </div>
        <p className="credits">
          Pictures: Utagawa Hiroshige, <i>Sudden Shower over Shin-Ōhashi Bridge and Atake</i>, 1857; Carleton E. Watkins,{" "}
          <i>Yosemite Valley from Union Point</i>, about 1872; Itō Jakuchū, <i>White Plum Blossoms and Moon</i>, 1755; Ogata
          Kōrin, <i>Irises at Yatsuhashi</i>, after 1709. The Metropolitan Museum of Art, Open Access, public domain.
        </p>
        <p className="credits">© 2026 dev-dami.</p>
        {/* The wordmark, as wide as the page. It stretches up as it scrolls
            in, and each letter springs taller under the pointer. */}
        <a href={REPO} className="mega" aria-label="Deck on GitHub">
          <span className="mega-word" aria-hidden>
            {["d", "e", "c", "k"].map((l, i) => (
              <span key={i} className="mega-letter">
                {l}
              </span>
            ))}
          </span>
          <span className="mega-cursor" aria-hidden />
        </a>
      </footer>
    </>
  );
}
