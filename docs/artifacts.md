# Artifacts in Deck

Ask Claude or Codex to create a document, diagram or small app as a Deck artifact.
For example: “Create a React pricing calculator as an artifact, with monthly and annual billing.”

The desktop opens new artifacts beside the conversation. Click an artifact's card in chat to reopen it, or choose **Artifacts** in the side panel. Ask either agent for changes; each successful edit saves a new revision.

- **Preview** shows the document or app. **Source** shows its original files.
- The revision picker shows who made each revision. **Restore** copies an earlier snapshot into a new revision.
- **Errors** shows build or runtime problems. Agents can read those reports and save a correction.
- **Download source** saves a single document directly, or a ZIP of the selected revision's source files for React and other multi-file artifacts. Compiled standalone export and public hosting are outside this release.

Supported formats are Markdown, HTML with local CSS/JavaScript/JSON/SVG files, SVG, and React JSX/TSX. React entry files default-export a component. Local components and CSS imports work; packaged imports are `react`, `react-dom`, `react-dom/client` and `react/jsx-runtime`. TypeScript is transpiled without type checking.

Artifacts run offline in an isolated view with no Deck IPC, Node or host filesystem access. Creating, editing and restoring source are unavailable in plan mode; saved source and diagnostics remain readable.

For agent authors, discover `artifact.create`, `artifact.update`, `artifact.restore`, `artifact.list`, `artifact.revisions` and `artifact.read` through Deck's search tool. Updates require the current `base_revision`; stale edits return a conflict so another agent's changes are preserved. Source reads are paged: pin the returned revision and follow `next_offset` until `eof`.
