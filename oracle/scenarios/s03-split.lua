return {
  purpose = 'split, vsplit, close and resize exercise win_pos/grid_destroy',
  width = 80,
  height = 24,
  steps = {
    { cmd = 'vsplit' },
    { cmd = 'split' },
    { cmd = 'resize 8' },
    { cmd = 'wincmd j' },
    { cmd = 'close' },
    { cmd = 'wincmd h' },
    { cmd = 'close' },
    { cmd = 'vsplit' },
  },
}
