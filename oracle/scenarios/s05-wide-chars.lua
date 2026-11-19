return {
  purpose = 'CJK, ZWJ emoji, combining characters, and double-width line-end wrapping',
  width = 40,
  height = 12,
  steps = {
    { lua = [[
      vim.api.nvim_buf_set_lines(0, 0, -1, false, {
        '世界 🌍 你好 e' .. string.char(0xcc, 0x81) .. ' ' .. string.char(0xcc, 0x81),
        'ZWJ: ' .. string.char(0xf0, 0x9f, 0x91, 0xa8) .. string.char(0xe2, 0x80, 0x8d)
          .. string.char(0xf0, 0x9f, 0x91, 0xa9) .. string.char(0xe2, 0x80, 0x8d)
          .. string.char(0xf0, 0x9f, 0x91, 0xa7) .. string.char(0xe2, 0x80, 0x8d)
          .. string.char(0xf0, 0x9f, 0x91, 0xa6) .. ' family, 中文 CJK',
        string.rep('界', 25),
        string.rep('A', 39) .. '世',
        'combining: a' .. string.char(0xcc, 0x81) .. 'b' .. string.char(0xcc, 0x81)
          .. 'c' .. string.char(0xcc, 0x81) .. ' 世',
      })
      vim.api.nvim_win_set_cursor(0, { 1, 0 })
    ]] },
    { input = 'G' },
    { input = 'gg' },
    { resize = { 24, 12 } },
    { resize = { 40, 12 } },
  },
}
