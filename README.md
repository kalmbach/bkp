# bkp

A terminal UI that mirrors one local directory into another, copying
files concurrently.

## Install

```
go install github.com/kalmbach/bkp@latest
```

Requires Go 1.26 or newer.

## Usage

```
bkp -i <dir> -o <dir> [-n N]
```

| Flag | Description |
|------|-------------|
| `-i` | Input directory. Required. |
| `-o` | Output directory. Required. |
| `-n` | Number of concurrent copies. Default 4. |
| `-v`, `-version` | Print the version and exit. |

`bkp` scans the input and shows how many files (and bytes) need copying.
Press space to start.

### Keys

| Key | Action |
|-----|--------|
| `space` | Start  and stops copying. |
| `q`, `esc` | Quit. Copies in progress are cancelled first. |

## How it works

- A file is copied when it is missing from the output, or its size or
  modification time differs. File contents are never compared.
- Files are never deleted from the output. A file removed or renamed in
  the input stays in the output under its old name.
- File mode and modification time are preserved.

## Limitations

- Local directories only. There is no network transfer.
