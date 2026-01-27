# go-remote-embed

A tool that downloads remote files and generates Go embed directives for them.

## Features

- Download files from remote URLs (HTTP/HTTPS)
- Copy local files to output directory
- Auto-generate `embed.go` with Go embed directives
- Configurable output paths with `<short_name>` placeholder support
- Auto-detect package name from `go.mod` or existing `.go` files

## Installation

```bash
go install github.com/zdunecki/go-remote-embed@latest
```

## Usage

1. Create an `embed.yaml` file in your project directory:

```yaml
output: ./.schemas
go-output: embed.go
go-mod: main
files:
  - "https://raw.githubusercontent.com/example/repo/main/schema.json"
  - "local/file.txt"
```

2. Run the tool:

```bash
//go:generate go-remote-embed
```

3. The tool will:
   - Download remote files (or copy local files) to the output directory
   - Generate an `embed.go` file with the appropriate `//go:embed` directives

## Configuration

| Field | Description | Default |
|-------|-------------|---------|
| `output` | Directory where files will be saved. Supports `<short_name>` placeholder. | `.` |
| `go-output` | Name of the generated Go file | `embed.go` |
| `go-mod` | Package name for the generated file | Auto-detected from `go.mod` or `.go` files |
| `files` | List of URLs or local file paths to embed | Required |

### Placeholder Support

The `output` field supports the `<short_name>` placeholder, which is replaced with the filename (without extension):

```yaml
output: assets/<short_name>
files:
  - "https://example.com/config.json"
```

This will save the file to `assets/config/config.json`.

## Example

See the [examples/basic](examples/basic) directory for a working example.

## License

MIT