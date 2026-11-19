return {
  purpose = ':hi, nvim_set_hl, colorscheme switch, extmark highlight and 0.12 attributes',
  width = 80,
  height = 24,
  steps = {
    { cmd = 'hi OracleA guifg=#ff0000 guibg=#000080 gui=bold,dim,overline' },
    { cmd = 'hi OracleB guifg=#00ff00 gui=italic,underline,strikethrough,altfont' },
    { cmd = 'normal! ihello highlighted' },
    { lua = [[
      vim.api.nvim_set_hl(0, 'OracleA', {
        fg = 0xffa500, bg = 0x101820, bold = true, dim = true,
        overline = true, underline = true, underdouble = true,
        underdotted = false, strikethrough = true, altfont = 3, blend = 10,
      })
      vim.api.nvim_set_hl(0, 'OracleB', {
        fg = 0x66ccff, bg = 0x202020, italic = true, reverse = true,
        undercurl = true, underdashed = true,
      })
      local ns = vim.api.nvim_create_namespace('oracle-highlight')
      vim.api.nvim_buf_set_extmark(0, ns, 0, 0, {
        end_col = 5, hl_group = 'OracleA', hl_eol = true,
      })
      vim.api.nvim_buf_set_extmark(0, ns, 0, 6, {
        end_col = 17, hl_group = 'OracleB',
      })
    ]] },
    { cmd = 'colorscheme desert' },
    { cmd = 'hi OracleA guifg=#abcdef gui=bold,blink' },
    { lua = [[
      vim.api.nvim_set_hl(0, 'OracleA', {
        fg = 0xabcdef, bold = true, blink = true, dim = true,
        conceal = true, overline = true, underdouble = true,
      })
    ]] },
  },
}
