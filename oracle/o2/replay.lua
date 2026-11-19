local script_source = debug.getinfo(1, 'S').source
local script_path = vim.fs.abspath(script_source:sub(1, 1) == '@' and script_source:sub(2) or script_source)
local o2_dir = vim.fs.dirname(script_path)
dofile(vim.fs.joinpath(o2_dir, 'shim.lua'))
local exporter = dofile(vim.fs.joinpath(o2_dir, 'export.lua'))

local input_path = arg[1]
assert(input_path, 'usage: nvim -l oracle/o2/replay.lua <inbound.mpack>')
local input_file = assert(io.open(input_path, 'rb'))
local inbound = input_file:read('*a')
input_file:close()

local function decode_ext(extension_kind, payload)
  return vim.mpack.decode(payload)
end

local function new_rpc_session()
  return vim.mpack.Session({
    unpack = vim.mpack.Unpacker({
      ext = {
        [0] = decode_ext,
        [1] = decode_ext,
        [2] = decode_ext,
      },
    }),
  })
end

local function decode_messages(data)
  local unpacker = vim.mpack.Unpacker({
    ext = {
      [0] = decode_ext,
      [1] = decode_ext,
      [2] = decode_ext,
    },
  })
  local position = 1
  local batches = {}
  while position <= #data do
    local message, next_position = unpacker(data, position)
    assert(next_position and next_position > position, 'invalid or incomplete inbound message')
    if message and message[1] == 2 and message[2] == 'redraw' then
      batches[#batches + 1] = message[3]
    end
    position = next_position
  end
  return batches
end

local function initial_size(batches)
  for batch_index, batch in ipairs(batches) do
    for update_index, update in ipairs(batch) do
      if update[1] == 'grid_resize' then
        local params = update[2]
        if params and params[1] == 1 then
          local width = params[2]
          local height = params[3]
          if width == nil or width == vim.NIL then width = 80 end
          if height == nil or height == vim.NIL then height = 24 end
          return width, height
        end
      end
    end
  end
  return 80, 24
end

local function make_fake_session()
  local color_map = vim.empty_dict()
  return {
    request = function(_self, method_name, ...)
      if method_name == 'nvim_get_color_map' then
        return true, color_map
      end
      if method_name == 'nvim_get_hl' then
        return true, vim.empty_dict()
      end
      if method_name == 'nvim_get_hl_id_by_name' then
        return true, 0
      end
      return false, 'unexpected API call: ' .. method_name
    end,
  }
end

local function initial_frame_state(batches, width, height)
  local Screen = require('test.functional.ui.screen')
  local replay_width, replay_height = initial_size(batches, width, height)
  local screen = Screen.new(replay_width, replay_height, {
    ext_linegrid = true,
    ext_multigrid = true,
    ext_hlstate = true,
    rgb = true,
  }, false)
  local state
  for _, batch in ipairs(batches) do
    for _, update in ipairs(batch) do
      screen:_redraw({ update })
      if update[1] == 'flush' then
        state = vim.json.decode(exporter.canonical(screen))
        break
      end
    end
    if state then break end
  end
  return state or vim.json.decode(exporter.canonical(screen))
end

local function make_fake_session_from_state(state)
  local color_map = vim.empty_dict()
  for _, attr in ipairs(state and state.attrs or {}) do
    for key, value in pairs(attr) do
      if type(key) == 'string' and type(value) == 'number' and key:match('ground') then
        color_map['Color' .. tostring(value)] = value
      end
    end
  end
  return {
    request = function(_self, method_name, ...)
      if method_name == 'nvim_get_color_map' then
        return true, color_map
      end
      if method_name == 'nvim_get_hl' then
        return true, vim.empty_dict()
      end
      if method_name == 'nvim_get_hl_id_by_name' then
        return true, 0
      end
      return false, 'unexpected API call: ' .. method_name
    end,
  }
end

_G.SESSION = make_fake_session()
local Screen = require('test.functional.ui.screen')
local batches = decode_messages(inbound)
local initial_width, initial_height = initial_size(batches)
local first_state = initial_frame_state(batches, initial_width, initial_height)
_G.SESSION = make_fake_session_from_state(first_state)
package.loaded['test.functional.ui.screen'] = nil
local ReplayScreen = require('test.functional.ui.screen')
local screen = ReplayScreen.new(initial_width, initial_height, {
  ext_linegrid = true,
  ext_multigrid = true,
  ext_hlstate = true,
  rgb = true,
}, false)
if first_state.default_colors then
  screen.default_colors = {
    rgb_fg = first_state.default_colors.fg,
    rgb_bg = first_state.default_colors.bg,
    rgb_sp = first_state.default_colors.sp,
  }
end

for batch_index, batch in ipairs(batches) do
  for update_index, update in ipairs(batch) do
    screen:_redraw({ update })
    if update[1] == 'flush' then
      io.write(exporter.canonical(screen), '\n')
      io.flush()
    end
  end
end
