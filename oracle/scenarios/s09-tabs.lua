return {
  purpose = 'tabnew/tabnext/tabclose exercise win_hide and grid reuse',
  width = 80,
  height = 24,
  steps = {
    { cmd = 'normal! ifirst tab' },
    { cmd = 'tabnew' },
    { cmd = 'normal! isecond tab' },
    { cmd = 'vsplit' },
    { cmd = 'tabnext' },
    { cmd = 'tabprevious' },
    { cmd = 'tabnew' },
    { cmd = 'normal! ithird tab' },
    { cmd = 'tabclose' },
    { cmd = 'tabnext' },
    { cmd = 'tabclose' },
  },
}
