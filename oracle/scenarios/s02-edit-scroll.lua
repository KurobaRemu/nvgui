return {
  purpose = 'insert 200 lines; <C-d>/<C-u>/G/gg exercise bidirectional grid_scroll',
  width = 80,
  height = 24,
  steps = {
    { lua = [[
      local lines = {}
      for i = 1, 200 do lines[i] = string.format('line %03d scroll probe', i) end
      vim.api.nvim_buf_set_lines(0, 0, -1, false, lines)
      vim.api.nvim_win_set_cursor(0, { 1, 0 })
    ]] },
    { input = '<C-d>' },
    { input = '<C-d>' },
    { input = '<C-u>' },
    { input = 'G' },
    { input = 'gg' },
    { input = '<C-d><C-d><C-u>Ggg' },
  },
}
