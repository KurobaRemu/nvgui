local source = debug.getinfo(1, 'S').source
local script_path = vim.fs.abspath(source:sub(1, 1) == '@' and source:sub(2) or source)
local gen_dir = vim.fs.dirname(script_path)
local oracle_dir = vim.fs.dirname(gen_dir)
local o2_dir = vim.fs.joinpath(oracle_dir, 'o2')
dofile(vim.fs.joinpath(o2_dir, 'shim.lua'))
local exporter = dofile(vim.fs.joinpath(o2_dir, 'export.lua'))

local scenario_path = arg[1]
local output_dir = arg[2]
assert(scenario_path and output_dir, 'usage: nvim -l oracle/gen/gen.lua <scenario.lua> <outdir>')

local function read_file(path)
  local file = assert(io.open(path, 'rb'))
  local content = file:read('*a')
  file:close()
  return content
end

local function write_file(path, content)
  local file = assert(io.open(path, 'wb'))
  file:write(content)
  file:close()
end

local function new_object()
  return vim.empty_dict()
end

local function json_scalar(value)
  return vim.json.encode(value)
end

local function canonical_value(value)
  if value == nil then
    return 'null'
  elseif value == vim.NIL then
    return 'null'
  elseif type(value) == 'number' or type(value) == 'string' or type(value) == 'boolean' then
    return json_scalar(value)
  elseif type(value) == 'table' then
    local keys = {}
    local items = {}
    for key, item in pairs(value) do
      if type(key) == 'number' and key >= 1 and key <= #value and key == math.floor(key) then
        items[key] = item
      elseif type(key) == 'string' and key ~= vim.type_idx and key ~= vim.val_idx then
        keys[#keys + 1] = key
      end
    end
    if #value > 0 then
      local parts = {}
      for index = 1, #value do
        parts[index] = canonical_value(items[index])
      end
      return '[' .. table.concat(parts, ',') .. ']'
    end
    table.sort(keys)
    local parts = {}
    for index, key in ipairs(keys) do
      parts[index] = json_scalar(key) .. ':' .. canonical_value(value[key])
    end
    return '{' .. table.concat(parts, ',') .. '}'
  end
  error('unsupported ui_events value: ' .. type(value))
end

local function ui_events_parameters(metadata)
  local events = {}
  for _, event in ipairs(metadata.ui_events or {}) do
    events[#events + 1] = {
      name = event.name,
      parameters = event.parameters,
    }
  end
  table.sort(events, function(left, right) return left.name < right.name end)
  return events
end

local function child_environment(temp_dir)
  local env = {}
  for key, value in pairs(vim.uv.os_environ()) do
    if not key:match('^XDG_') and key ~= 'LANG' and key ~= 'LC_ALL' and key ~= 'TZ' then
      env[#env + 1] = key .. '=' .. value
    end
  end
  for _, key in ipairs({ 'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME', 'XDG_CACHE_HOME', 'XDG_RUNTIME_DIR' }) do
    local path = vim.fs.joinpath(temp_dir, key)
    vim.fn.mkdir(path, 'p')
    env[#env + 1] = key .. '=' .. path
  end
  env[#env + 1] = 'LANG=en_US.UTF-8'
  env[#env + 1] = 'LC_ALL=en_US.UTF-8'
  env[#env + 1] = 'TZ=UTC'
  return env
end

local function decode_ext(_kind, payload)
  return vim.mpack.decode(payload)
end

local function decode_messages(data)
  local unpacker = vim.mpack.Unpacker({
    ext = { [0] = decode_ext, [1] = decode_ext, [2] = decode_ext },
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

local function initial_size(batches, default_width, default_height)
  for _, batch in ipairs(batches) do
    for _, update in ipairs(batch) do
      if update[1] == 'grid_resize' then
        local params = update[2]
        if params and params[1] == 1 and params[2] and params[3] then
          return params[2], params[3]
        end
      end
    end
  end
  return default_width, default_height
end

local function replay_frames(inbound, width, height)
  local color_map = _G.REPLAY_CONTEXT.color_map
  local highlights = _G.REPLAY_CONTEXT.highlights
  local fake_session = {
    request = function(_self, method_name)
      if method_name == 'nvim_get_color_map' then return true, color_map end
      if method_name == 'nvim_get_hl' then return true, highlights end
      if method_name == 'nvim_get_hl_id_by_name' then return true, 0 end
      return false, 'unexpected API call: ' .. method_name
    end,
  }
  local saved_session = _G.SESSION
  _G.SESSION = fake_session
  local ok, replay_result = pcall(function()
    package.loaded['test.functional.ui.screen'] = nil
    local Screen = require('test.functional.ui.screen')
    local batches = decode_messages(inbound)
    local replay_width, replay_height = initial_size(batches, width, height)
    local screen = Screen.new(replay_width, replay_height, {
      ext_linegrid = true,
      ext_multigrid = true,
      ext_hlstate = true,
      rgb = true,
    }, false)
    local frames = {}
    local redraw_count = 0
    local flush_count = 0
    for _, batch in ipairs(batches) do
      redraw_count = redraw_count + 1
      for _, update in ipairs(batch) do
        screen:_redraw({ update })
        if update[1] == 'flush' then
          flush_count = flush_count + 1
          frames[#frames + 1] = exporter.canonical(screen)
        end
      end
    end
    return { frames = frames, redraw_count = redraw_count, flush_count = flush_count, screen = screen }
  end)
  _G.SESSION = saved_session
  assert(ok, replay_result)
  return replay_result.frames, replay_result.redraw_count, replay_result.flush_count, replay_result.screen
end

local function render_grid_text(grid)
  local lines = {}
  for row_index = 1, grid.height do
    local row = grid.rows[row_index] or {}
    local texts = {}
    for column_index = 1, grid.width do
      local text = row[column_index] and row[column_index].text or ' '
      if text == '' then text = '\226\128\139' end
      texts[column_index] = text
    end
    lines[row_index] = table.concat(texts)
  end
  return lines
end

local function grid_ids(screen)
  local ids = {}
  for grid_id in pairs(screen._grids) do
    if type(grid_id) == 'number' then ids[#ids + 1] = grid_id end
  end
  table.sort(ids)
  return ids
end

local function frame_text_view(screen)
  local view = new_object()
  for _, grid_id in ipairs(grid_ids(screen)) do
    view[tostring(grid_id)] = render_grid_text(screen._grids[grid_id])
  end
  return view
end

local scenario = assert(loadfile(scenario_path))()
local scenario_name = vim.fs.basename(scenario_path):gsub('%.lua$', '')
local width = scenario.width or 80
local height = scenario.height or 24
local steps = scenario.steps or {}
vim.fn.mkdir(output_dir, 'p')
local temp_dir = vim.fs.joinpath(vim.uv.os_tmpdir(), 'nvgui-gen-' .. scenario_name .. '-' .. tostring(vim.uv.hrtime()))
vim.fn.mkdir(temp_dir, 'p')

local ui_options = {
  ext_linegrid = true,
  ext_multigrid = true,
  ext_hlstate = true,
  rgb = true,
}
local version_object = vim.system({ 'nvim', '--version' }, { text = true }):wait()
assert(version_object.code == 0, version_object.stderr)
local version_first = vim.split(version_object.stdout, '\n', { plain = true })[1]
local screen_lua_path = vim.fs.joinpath(oracle_dir, 'neovim-test', 'test', 'functional', 'ui', 'screen.lua')
local screen_lua_sha256 = vim.fn.sha256(read_file(screen_lua_path))

local function cleanup()
  if raw_file then
    pcall(function() raw_file:flush() end)
  end
  if proc then
    pcall(function() proc:close('TERM') end)
    pcall(function() proc:close() end)
  end
  pcall(function() vim.fs.rm(temp_dir, { recursive = true, force = true }) end)
end

local proc
local raw_file

local generation_ok, generation_error = xpcall(function()

local uv_stream = require('test.client.uv_stream')
local Session = require('test.client.session')
proc = uv_stream.ProcStream.spawn({ 'nvim', '--embed', '--clean', '-n' }, child_environment(temp_dir))
local raw_path = vim.fs.joinpath(temp_dir, 'inbound.mpack')
raw_file = assert(io.open(raw_path, 'wb'))
local original_read_start = proc.read_start
proc.read_start = function(self, on_stdout, on_stderr)
  return original_read_start(self, function(chunk)
    if chunk then raw_file:write(chunk) end
    on_stdout(chunk)
  end, on_stderr)
end
local session = Session.new(proc)
_G.SESSION = session

local function request(method_name, ...)
  local ok, result = session:request(method_name, ...)
  assert(ok, method_name .. ': ' .. vim.inspect(result))
  return result
end

local function pump(timeout_ms, step_end)
  local start = vim.uv.now()
  local deadline = start + timeout_ms
  local saw_redraw = false
  local saw_flush = false
  local function notification_callback(method_name, args)
    if method_name ~= 'redraw' then return end
    saw_redraw = true
    local batch_has_flush = false
    for _, update in ipairs(args) do
      if update[1] == 'flush' then batch_has_flush = true end
    end
    if step_end and batch_has_flush then
      saw_flush = true
      session:stop()
    end
  end
  repeat
    local remaining = math.max(0, deadline - vim.uv.now())
    session:run(nil, notification_callback, nil, remaining == 0 and 1 or remaining)
    if step_end and saw_flush then break end
  until vim.uv.now() >= deadline or (not step_end and not saw_redraw)
  if step_end and not saw_flush then
    session:run(nil, notification_callback, nil, 1)
  end
  return saw_redraw, saw_flush
end

local api_info = request('nvim_get_api_info')
local api_level = api_info[2].version.api_level
local ui_events_hash = vim.fn.sha256(canonical_value(ui_events_parameters(api_info[2])))
local color_map = request('nvim_get_color_map')
local highlights = request('nvim_get_hl', 0, vim.empty_dict())
_G.REPLAY_CONTEXT = { color_map = color_map, highlights = highlights }
request('nvim_ui_attach', width, height, ui_options)
pump(2000, true)

local step_frames = {}
local inconclusive = false
for step_index, step in ipairs(steps) do
  if step.cmd then
    request('nvim_command', step.cmd)
  elseif step.input then
    request('nvim_input', step.input)
  elseif step.lua then
    request('nvim_exec_lua', step.lua, {})
  elseif step.resize then
    request('nvim_ui_try_resize', step.resize[1], step.resize[2])
    width = step.resize[1]
    height = step.resize[2]
  else
    error('unsupported step #' .. step_index)
  end
  local redraw_options = vim.empty_dict()
  redraw_options.flush = true
  request('nvim__redraw', redraw_options)
  request('nvim_eval', '1')
  local _saw_redraw, saw_flush = pump(2000, true)
  raw_file:flush()
  local inbound = read_file(raw_path)
  local frames, redraw_count, flush_count = replay_frames(inbound, width, height)
  step_frames[step_index] = flush_count
  if scenario_name == 's10-bulk-delete' and step_index == #steps and not saw_flush then
    inconclusive = true
  end
end

session:notify('nvim_command', 'qa!')
session:run(nil, function() end, nil, 1000)
session:close()
raw_file:close()

local inbound = read_file(raw_path)
write_file(vim.fs.joinpath(output_dir, 'inbound.mpack'), inbound)
local frames, redraw_count, flush_count, final_screen = replay_frames(inbound, width, height)
write_file(vim.fs.joinpath(output_dir, 'frames.jsonl'), table.concat(frames, '\n') .. (#frames > 0 and '\n' or ''))

local meta = new_object()
meta.scenario = scenario_name
meta.purpose = scenario.purpose or ''
meta.nvim_version = version_first
meta.api_level = api_level
meta.ui_events_sha256 = ui_events_hash
meta.screen_lua_sha256 = screen_lua_sha256
meta.ui_options = ui_options
meta.width = width
meta.height = height
meta.redraw_count = redraw_count
meta.flush_count = flush_count
meta.frame_count = #frames
meta.step_end_frames = step_frames
meta.inconclusive = inconclusive
meta.step_count = #steps
write_file(vim.fs.joinpath(output_dir, 'meta.json'), vim.json.encode(meta) .. '\n')

local index = new_object()
index.scenario = scenario_name
index.purpose = scenario.purpose or ''
index.frame_count = #frames
index.inconclusive = inconclusive
index.final_grids = frame_text_view(final_screen)
  write_file(vim.fs.joinpath(output_dir, 'index.json'), vim.json.encode(index) .. '\n')
  print(vim.json.encode({ scenario = scenario_name, frames = #frames, redraw_count = redraw_count, inconclusive = inconclusive }))
end, debug.traceback)

cleanup()
if not generation_ok then
  error(generation_error, 0)
end
