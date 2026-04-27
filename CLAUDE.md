# Claude Code project guide

When you work on UI changes in spk-mail, read **`docs/ui-testing.md` first** — it documents how to launch the app in browser mode against an in-process mock IMAP server, the fixtures, and the `/api/_test/*` automation routes.

The end-to-end manual verification rule from the global CLAUDE.md applies here. For UI work, exercise the change in `--browser` mode (or via the Playwright suite under `tests/playwright/`) before reporting it as done.

Architecture and tech-stack details live in `docs/superpowers/specs/2026-04-27-spk-mail-design.md`.
