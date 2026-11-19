return {
  purpose = 'attach 80x24, initial highlight table, default_colors_set',
  width = 80,
  height = 24,
  steps = {
    { cmd = 'set laststatus=2' },
    { cmd = 'highlight OracleBoot guifg=#ff5555 guibg=#202020 gui=bold' },
    { cmd = 'normal! iattach' },
    { cmd = 'echo "boot"' },
  },
}
