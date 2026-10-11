# Rules for coding agents

These rules apply to every change in this repository.

## Copy and comments

- All prose (README, docs, doc comments, error messages, command output, changelog) is Australian
  English, in sentence case, with no emoji.
- Never write "get" in prose: name the thing ("the customer's entitlements", "Quick start").
  Avoid vague "what" clauses.
- Error strings start in lower case (except proper nouns such as Entitler) and end without a full
  stop. Messages from the API are passed on unchanged.
- Every exported identifier has a doc comment. Write no other comments: rename, extract or
  restructure code that needs one. Lint directives are the only exception, and each carries a
  reason.
- Every snippet in the README and `docs/` compiles: keep it in `docs_examples_test.go` or in an
  example program, line for line (`TestEverySnippetCompiles` checks).
- Keep the SDK light: standard library only, and no code, abstraction or dependency that no
  requirement needs.

## Before pushing

Run every command in [CONTRIBUTING.md](CONTRIBUTING.md#commands), and the live API tests when you
have `ENTITLER_TEST_KEY`. The repository's only required check is `All checks passed`; every other
job in `ci.yml` is in its `needs`.

## Authorship and pull requests

- Every commit is authored and committed by the repository owner. Before your first commit, run:

  ```sh
  git config user.name "Romain Francez"
  git config user.email "1330814+romainfrancez@users.noreply.github.com"
  ```

- Commits carry no assistant attribution: no `Co-Authored-By` or session trailers, no "Generated
  with" line, no session link and no model name. Check `git log --format='%an <%ae>%n%b'` before
  every push.
- No file, commit or branch names the tool that wrote it, and this file is the only instruction
  file for agents.
- Push your branch only. Do not open, edit or comment on pull requests or issues, and never create
  tags or publish a release; the owner does.
