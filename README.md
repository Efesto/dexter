# Dexter

A fast Elixir go-to-definition engine that runs as an LSP server. Built for large codebases where traditional Elixir LSP servers are too slow.

Dexter indexes every module and function definition in your project into a local SQLite database, then serves instant go-to-definition responses over the Language Server Protocol. It understands aliases, imports, `defdelegate`, nested modules, and heredocs.

## Why?

Elixir LSP servers (ElixirLS, Lexical, etc.) can struggle with very large umbrella apps. Ctags works but doesn't understand Elixir module namespacing, so `Foo` often resolves to the wrong module. Dexter sits in between — it's Elixir-aware but doesn't try to be a full LSP. Just fast, correct go-to-definition.

Dexter is designed to run **alongside** your existing Elixir LSP, not replace it. Use dexter for fast navigation and your full LSP for diagnostics, completions, and refactoring.

## Install

### From source

Requires Go 1.21+ and Xcode command line tools (for SQLite via CGo).

```sh
git clone git@gitlab.com:remote-com/employ-starbase/dexter.git
cd dexter
go build -o dexter ./cmd/
cp dexter /usr/local/bin/
```

### With mise

```sh
mise plugin add dexter git@gitlab.com:remote-com/employ-starbase/dexter.git
mise install dexter@latest
mise use dexter@latest
```

Or add to your `.mise.toml`:

```toml
[plugins]
dexter = "git@gitlab.com:remote-com/employ-starbase/dexter.git"

[tools]
dexter = "latest"
```

## Quick start

```sh
# 1. Index your project (one-time, ~8s for a large codebase)
cd ~/code/my-elixir-project
dexter init .

# 2. Add .dexter.db to your .gitignore
echo ".dexter.db" >> .gitignore

# 3. Configure your editor (see below)
```

## Editor setup

### Neovim (0.11+)

Add to your LSP configuration (e.g., `after/plugin/lsp.lua`):

```lua
vim.lsp.config('dexter', {
  cmd = { 'dexter', 'lsp' },
  root_markers = { '.dexter.db', 'mix.exs', '.git' },
  filetypes = { 'elixir', 'eelixir' },
})

vim.lsp.enable 'dexter'
```

That's it. Go-to-definition (`gd`, `<C-]>`, or whatever you have mapped to `vim.lsp.buf.definition()`) will now use dexter alongside any other attached LSP servers.

If you want a dedicated binding just for dexter:

```lua
vim.keymap.set("n", "<leader>va", function()
  vim.lsp.buf.definition({ filter = function(client) return client.name == "dexter" end })
end)
```

### Neovim (with nvim-lspconfig)

```lua
local lspconfig = require("lspconfig")
local configs = require("lspconfig.configs")

configs.dexter = {
  default_config = {
    cmd = { "dexter", "lsp" },
    filetypes = { "elixir", "eelixir" },
    root_dir = lspconfig.util.root_pattern(".dexter.db", "mix.exs", ".git"),
  },
}

lspconfig.dexter.setup({})
```

### VS Code / Cursor

Install the [Generic LSP Client](https://marketplace.visualstudio.com/items?itemName=llllvvuu.llm-lsp) extension (or any extension that lets you configure a custom LSP server), then add to your `settings.json`:

```json
{
  "genericLSP.serverCommand": "dexter",
  "genericLSP.serverArgs": ["lsp"],
  "genericLSP.languages": ["elixir"]
}
```

Alternatively, create a `.vscode/settings.json` in your project:

```json
{
  "elixir.languageServerOverride": {
    "command": "dexter",
    "args": ["lsp"]
  }
}
```

The exact configuration depends on which LSP client extension you use. The key is: the command is `dexter lsp`, communicating over stdio.

### Any editor with LSP support

Dexter speaks standard LSP over stdio. Configure your editor to run:

```
dexter lsp
```

as a language server for `elixir` files. It advertises `definitionProvider` and `textDocumentSync`.

## CLI usage

The CLI commands are still available for scripting and manual use.

### Index a project

```sh
# First time — indexes all .ex/.exs files (including deps/)
dexter init ~/code/my-elixir-project

# Re-init from scratch (deletes existing index)
dexter init --force ~/code/my-elixir-project
```

### Look up definitions

```sh
# Find where a module is defined
dexter lookup MyApp.Repo
# => /path/to/lib/my_app/repo.ex:1

# Find where a function is defined (follows defdelegates by default)
dexter lookup MyApp.Repo get
# => /path/to/lib/my_app/repo.ex:15

# Don't follow defdelegates
dexter lookup --no-follow-delegates MyApp.Accounts fetch
# => /path/to/lib/my_app/accounts.ex:5

# Strict mode — exit 1 if exact function not found (no fallback to module)
dexter lookup --strict MyApp.Repo nonexistent
# => (exit code 1)
```

### Keep the index up to date

```sh
# Re-index a single file (~10ms)
dexter reindex /path/to/lib/my_app/repo.ex

# Re-index the whole project (only re-parses changed files)
dexter reindex ~/code/my-elixir-project
```

When running as an LSP server, dexter automatically:
- Reindexes files on save (`textDocument/didSave`)
- Runs an incremental reindex on startup
- Watches `.git/HEAD` for branch switches and reindexes when detected

## Features

- **Alias resolution** — `alias MyApp.Handlers.Foo`, `alias MyApp.Handlers.Foo, as: Cool`, `alias MyApp.Handlers.{Foo, Bar}`
- **Import resolution** — bare function calls resolved through `import` declarations
- **Delegate following** — `defdelegate fetch(id), to: MyApp.Repo` jumps to `MyApp.Repo.fetch`, respecting `as:` renames
- **Local buffer search** — private function calls resolve without leaving the current file
- **All def forms** — `def`, `defp`, `defmacro`, `defmacrop`, `defguard`, `defguardp`, `defdelegate`, `defprotocol`, `defimpl`, `defstruct`, `defexception`
- **Heredoc awareness** — code examples in `@moduledoc`/`@doc` are skipped
- **Module nesting** — correctly tracks `end` keywords to attribute functions to the right module
- **Git branch detection** — automatically reindexes when you switch branches
- **Parallel indexing** — uses all CPU cores for initial index

## How it works

1. **Parsing** — `.ex`/`.exs` files are scanned line-by-line with regex for definition declarations. The parser tracks module nesting, heredoc boundaries, and aliases for `defdelegate` resolution.

2. **Storage** — Definitions are stored in SQLite (`.dexter.db`) with indexes on module name and module+function for fast lookups.

3. **LSP server** — `dexter lsp` speaks JSON-RPC over stdio. On `textDocument/definition`, it parses the cursor context, resolves aliases and imports from the open buffer, and queries the index.

4. **Incremental updates** — File mtimes are tracked. Reindex only re-parses files that changed.

## Performance

Measured on a 57k-file Elixir umbrella app (2.5M lines, 340k+ definitions):

| Operation | Time |
|-----------|------|
| Full init | ~8s |
| Lookup (LSP or CLI) | ~10ms |
| Single file reindex (on save) | ~10ms |
| Full reindex (no changes) | ~2s |
