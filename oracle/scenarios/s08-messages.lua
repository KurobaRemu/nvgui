return {
  purpose = 'multi-line :echo message scrolling and clearing; msg_set_pos scrolled',
  width = 80,
  height = 24,
  steps = {
    { cmd = 'set cmdheight=5' },
    { cmd = 'echo "one\\ntwo\\nthree\\nfour"' },
    { cmd = 'echom "history line"' },
    { cmd = 'echo "fifth\\nsixth\\nseventh"' },
    { cmd = 'messages clear' },
    { cmd = 'echom "after clear"' },
    { cmd = 'echo ""' },
    { cmd = 'messages' },
  },
}
