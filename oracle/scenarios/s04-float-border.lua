return {
  purpose = 'two overlapping bordered floats zindex 50/60, move and close; compindex',
  width = 80,
  height = 24,
  steps = {
    { lua = [[
      local first = vim.api.nvim_create_buf(false, true)
      local second = vim.api.nvim_create_buf(false, true)
      vim.api.nvim_buf_set_lines(first, 0, -1, false, { 'zindex 50', 'first float' })
      vim.api.nvim_buf_set_lines(second, 0, -1, false, { 'zindex 60', 'second float' })
      _G.oracle_first_win = vim.api.nvim_open_win(first, false, {
        relative = 'editor', row = 3, col = 8, width = 28, height = 6,
        border = 'single', zindex = 50,
      })
      _G.oracle_second_win = vim.api.nvim_open_win(second, false, {
        relative = 'editor', row = 6, col = 16, width = 30, height = 6,
        border = 'single', zindex = 60,
      })
    ]] },
    { lua = [[
      vim.api.nvim_win_set_config(_G.oracle_second_win, {
        relative = 'editor', row = 8, col = 20, width = 30, height = 6,
        border = 'double', zindex = 60,
      })
    ]] },
    { lua = [[
      vim.api.nvim_win_set_config(_G.oracle_first_win, {
        relative = 'editor', row = 4, col = 10, width = 28, height = 6,
        border = 'rounded', zindex = 50,
      })
    ]] },
    { lua = [[
      vim.api.nvim_win_close(_G.oracle_second_win, true)
    ]] },
    { lua = [[
      vim.api.nvim_win_close(_G.oracle_first_win, true)
    ]] },
  },
}
