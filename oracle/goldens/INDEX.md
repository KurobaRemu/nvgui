# M0.2 goldens

由 `nvim -l oracle/gen/gen.lua <scenario.lua> <outdir>` 在固定 `LANG=en_US.UTF-8`、`TZ=UTC`、独立 XDG 临时目录、`nvim --embed --clean -n` 环境生成。每个目录含 `inbound.mpack`（原始 stdout 字节）、`frames.jsonl`（逐 flush 一帧 Canonical JSON）、`meta.json`（版本、API 与步骤帧号）。

下面“末帧画面”按 screen.lua 内部每个 grid 的纯文本逐行列出；`U+200B` 表示双宽字符右侧的空串延续格。

## s01-boot

- 目的：attach 80x24, initial highlight table, default_colors_set
- 帧数：5；redraw notifications：10；steps：4；inconclusive：false
- 每步结束帧号：2, 3, 4, 5

### grid 1

```text






















[No Name] [+]                                                 1,6            All

```

### grid 2

```text
attach
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
```

### grid 3

```text
boot























```

## s02-edit-scroll

- 目的：insert 200 lines; <C-d>/<C-u>/G/gg exercise bidirectional grid_scroll
- 帧数：14；redraw notifications：20；steps：7；inconclusive：false
- 每步结束帧号：2, 4, 6, 8, 10, 12, 14

### grid 1

```text






















[No Name] [+]                                                 1,1            Top

```

### grid 2

```text
line 001 scroll probe
line 002 scroll probe
line 003 scroll probe
line 004 scroll probe
line 005 scroll probe
line 006 scroll probe
line 007 scroll probe
line 008 scroll probe
line 009 scroll probe
line 010 scroll probe
line 011 scroll probe
line 012 scroll probe
line 013 scroll probe
line 014 scroll probe
line 015 scroll probe
line 016 scroll probe
line 017 scroll probe
line 018 scroll probe
line 019 scroll probe
line 020 scroll probe
line 021 scroll probe
line 022 scroll probe
```

### grid 3

```text
























```

## s03-split

- 目的：split, vsplit, close and resize exercise win_pos/grid_destroy
- 帧数：8；redraw notifications：13；steps：8；inconclusive：false
- 每步结束帧号：2, 3, 4, 5, 6, 6, 7, 8

### grid 1

```text
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
[No Name]             0,0-1          All│
                                        │
                                        │
[No Name]             0,0-1          All│
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
[No Name]             0,0-1          All [No Name]            0,0-1          All

```

### grid 2

```text

~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
```

### grid 3

```text
























```

### grid 6

```text

~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
```

## s04-float-border

- 目的：two overlapping bordered floats zindex 50/60, move and close; compindex
- 帧数：7；redraw notifications：12；steps：5；inconclusive：false
- 每步结束帧号：3, 4, 5, 6, 7

### grid 1

```text






















[No Name]                                                     0,0-1          All

```

### grid 2

```text

~
~
~                                    │ ╲ ││
~                                    ││╲╲││
~                                    ││ ╲ │
~
~                                 NVIM v0.12.6
~                 ────────────────────────────────────────────
~                 Nvim is open source and freely distributable
~                           https://neovim.io/#chat
~                 ────────────────────────────────────────────
~                 type  :help nvim<Enter>     if you are new!
~                 type  :checkhealth<Enter>   to optimize Nvim
~                 type  :q<Enter>             to exit
~                 type  :help<Enter>          for help
~                 ────────────────────────────────────────────
~                 type  :help news<Enter>     for v0.12 notes
~                 ────────────────────────────────────────────
~                        Help poor children in Uganda!
~                 type  :help Kuwasha<Enter>  for information
~
```

### grid 3

```text
























```

## s05-wide-chars

- 目的：CJK, ZWJ emoji, combining characters, and double-width line-end wrapping
- 帧数：10；redraw notifications：14；steps：5；inconclusive：false
- 每步结束帧号：2, 4, 6, 8, 10

### grid 1

```text










[No Name] [+]         1,1            All

```

### grid 2

```text
世​界​ 🌍​ 你​好​ é ́
ZWJ: 👨‍👩‍👧‍👦​ family, 中​文​ CJK
界​界​界​界​界​界​界​界​界​界​界​界​界​界​界​界​界​界​界​界​
界​界​界​界​界​
AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA>
世​
combining: áb́ć 世​
~
~
~
```

### grid 3

```text












```

## s06-highlight

- 目的：:hi, nvim_set_hl, colorscheme switch, extmark highlight and 0.12 attributes
- 帧数：8；redraw notifications：19；steps：7；inconclusive：false
- 每步结束帧号：2, 3, 4, 5, 6, 7, 8

### grid 1

```text






















[No Name] [+]                                                 1,17           All

```

### grid 2

```text
hello highlighted
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
```

### grid 3

```text
























```

## s07-resize

- 目的：nvim_ui_try_resize enlarge/shrink below content size
- 帧数：14；redraw notifications：21；steps：7；inconclusive：false
- 每步结束帧号：2, 4, 6, 8, 10, 12, 14

### grid 1

```text
















[No Name] [+]                             40,1           Bot

```

### grid 2

```text
resize 37 0123456789012345678901234567890123456789
resize 38 0123456789012345678901234567890123456789
resize 39 0123456789012345678901234567890123456789
resize 40 0123456789012345678901234567890123456789
~
~
~
~
~
~
~
~
~
~
~
~
```

### grid 3

```text


















```

## s08-messages

- 目的：multi-line :echo message scrolling and clearing; msg_set_pos scrolled
- 帧数：8；redraw notifications：13；steps：8；inconclusive：false
- 每步结束帧号：2, 3, 4, 5, 5, 6, 7, 8

### grid 1

```text


















[No Name]                                                     0,0-1          All



[No Name]                                                     0,0-1          All

```

### grid 2

```text

~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
```

### grid 3

```text
after clear























```

## s09-tabs

- 目的：tabnew/tabnext/tabclose exercise win_hide and grid reuse
- 帧数：12；redraw notifications：17；steps：11；inconclusive：false
- 每步结束帧号：2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12

### grid 1

```text
 + [No Name]  2+ [No Name]              │                                      X
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
                                        │
[No Name] [+]         1,10           All [No Name] [+]        1,10           All

```

### grid 3

```text
























```

### grid 4

```text
second tab
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
```

### grid 5

```text
second tab
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
```

## s10-bulk-delete

- 目的：delete 600 lines (upstream #26980 watchdog fixture)
- 帧数：4；redraw notifications：9；steps：3；inconclusive：false
- 每步结束帧号：2, 3, 4

### grid 1

```text






















[No Name] [+]                                                 1,8            All

```

### grid 2

```text
watchdog
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
~
```

### grid 3

```text
























```
