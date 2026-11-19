return {
  purpose = 'nvim_ui_try_resize enlarge/shrink below content size',
  width = 60,
  height = 18,
  steps = {
    { lua = [[
      local lines = {}
      for i = 1, 40 do lines[i] = string.format('resize %02d 0123456789012345678901234567890123456789', i) end
      vim.api.nvim_buf_set_lines(0, 0, -1, false, lines)
    ]] },
    { resize = { 100, 30 } },
    { input = 'G' },
    { resize = { 72, 20 } },
    { resize = { 30, 9 } },
    { resize = { 20, 5 } },
    { resize = { 60, 18 } },
  },
}
