# Virayeshgar
Dead simple modal editor based off vim motions

⚠️ WIP: This project was started to relieve boredom. It is full of bug and written in the wrost way possible =)))

## Installation
```bash
go install github.com/amirali/virayeshgar@latest
```

## Todo

### features
- [x] line number
- [ ] relative line number
- [x] status bar
- [ ] structured and modular status bar
- [x] syntax highlighting

### navigation
- [x] hjkl
- [x] `{` and `}` paragraph jumps
- [ ] `:N` go to line N
- [ ] `Nh`, `Nj`, `Nk`, `Nl` to navigate by N

### actions
- [x] `i` and `I` insert mode
- [x] `a` and `A` insert mode
- [x] `o` and `O` insert mode
- [x] `/` search mode
- [x] `dd` cut single line
- [x] `x` cut single character
- [ ] `Ndd` cut N lines
- [x] `yy` yank single line
- [ ] `Nyy` yank N lines
- [x] `p` and `P` paste single line
- [x] `u` undo
- [ ] `ctrl+r` redo
- [ ] `ce` insert mode
- [ ] `ci` insert mode

### modes
- [x] insert mode
- [x] normal mode
- [x] command mode
- [ ] visual mode

### commands
- [x] `w` write
- [x] `q` quit
- [x] `wq` write and quit
- [x] `q!` quit without write
