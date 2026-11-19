return {
  purpose = 'delete 600 lines (upstream #26980 watchdog fixture)',
  width = 80,
  height = 24,
  steps = {
    { lua = [[
      local lines = {}
      for i = 1, 600 do lines[i] = string.format('bulk delete %03d', i) end
      vim.api.nvim_buf_set_lines(0, 0, -1, false, lines)
      vim.api.nvim_win_set_cursor(0, { 1, 0 })
    ]] },
    { cmd = 'silent! normal! 600dd' },
    { cmd = 'normal! iwatchdog' },
  },
}
