local exporter = {}

local function new_object()
  return vim.empty_dict()
end

local function present(value)
  return value ~= nil and value ~= vim.NIL
end

local function value_or(value, fallback)
  if value == nil or value == vim.NIL then
    return fallback
  end
  return value
end

local function json_scalar(value)
  return vim.json.encode(value)
end

local function sorted_keys(value)
  local keys = {}
  for key in pairs(value) do
    if type(key) == 'string' and key ~= vim.type_idx and key ~= vim.val_idx then
      keys[#keys + 1] = key
    end
  end
  table.sort(keys)
  return keys
end

local function encode_object(source, keys)
  if source == nil or source == vim.NIL then
    return 'null'
  end
  local parts = {}
  local key_count = 0
  for _, key in ipairs(keys) do
    if source[key] ~= nil and source[key] ~= vim.NIL then
      key_count = key_count + 1
      parts[key_count] = vim.json.encode(key) .. ':' .. vim.json.encode(source[key])
    end
  end
  return '{' .. table.concat(parts, ',') .. '}'
end

local function encode_pairs(pairs)
  local parts = {}
  for index, pair in ipairs(pairs) do
    parts[index] = vim.json.encode(pair[1]) .. ':' .. pair[2]
  end
  return '{' .. table.concat(parts, ',') .. '}'
end

local function copy_object(value)
  local result = {}
  if value == nil or value == vim.NIL or type(value) ~= 'table' then
    return '{}'
  end
  for key, item in pairs(value) do
    if type(key) == 'string' and key ~= vim.type_idx and key ~= vim.val_idx and item ~= nil and item ~= vim.NIL then
      result[key] = item
    end
  end
  return encode_object(result, sorted_keys(result))
end

local function attr_key(attr)
  return attr
end

local function sorted_grid_ids(grids)
  local ids = {}
  for grid_id in pairs(grids) do
    if type(grid_id) == 'number' then
      ids[#ids + 1] = grid_id
    end
  end
  table.sort(ids)
  return ids
end

function exporter.canonical(screen)
  local default_colors_source = screen.default_colors or {}
  local default_colors = {}
  default_colors.fg = value_or(default_colors_source.rgb_fg, 0)
  default_colors.bg = value_or(default_colors_source.rgb_bg, 0)
  default_colors.sp = value_or(default_colors_source.rgb_sp, 0)

  local attrs = { '{}' }
  local attr_indices = { ['{}'] = 0 }
  local grids = {}
  local placements = {}
  local grid_ids = sorted_grid_ids(screen._grids)

  for grid_index, grid_id in ipairs(grid_ids) do
    local grid = screen._grids[grid_id]
    local rows = {}
    for row_index = 1, grid.height do
      local row = grid.rows[row_index] or {}
      local canonical_row = {}
      for column_index = 1, grid.width do
        local cell = row[column_index] or {}
        local highlight_id = value_or(cell.hl_id, 0)
        local attr_definition = screen._attr_table[highlight_id]
        local rgb_attr = attr_definition and attr_definition[1] or nil
        local attr_value = copy_object(rgb_attr)
        local attr_serialization = attr_key(attr_value)
        local attr_index = attr_indices[attr_serialization]
        if attr_index == nil then
          attrs[#attrs + 1] = attr_value
          attr_index = #attrs - 1
          attr_indices[attr_serialization] = attr_index
        end
        canonical_row[column_index] = { value_or(cell.text, ' '), attr_index }
      end
      rows[row_index] = canonical_row
    end

    local grid_value = {}
    grid_value.w = grid.width
    grid_value.h = grid.height
    grid_value.rows = rows
    grids[tostring(grid_id)] = grid_value

    local win_position = screen.win_position and screen.win_position[grid_id] or nil
    if present(win_position) then
      local placement = {}
      placement.kind = 'win'
      placement.win = value_or(win_position.win, 0)
      placement.row = value_or(win_position.startrow, 0)
      placement.col = value_or(win_position.startcol, 0)
      placement.width = value_or(win_position.width, 0)
      placement.height = value_or(win_position.height, 0)
      placements[tostring(grid_id)] = placement
    else
      local float_position = screen.float_pos and screen.float_pos[grid_id] or nil
      if present(float_position) then
        local placement = {}
        if float_position.external == true then
          placement.kind = 'external'
        else
          placement.kind = 'float'
          placement.win = value_or(float_position[1], 0)
          placement.anchor = value_or(float_position[2], '')
          placement.anchor_grid = value_or(float_position[3], 0)
          placement.anchor_row = value_or(float_position[4], 0)
          placement.anchor_col = value_or(float_position[5], 0)
          placement.mouse = value_or(float_position[6], false)
          placement.zindex = value_or(float_position[7], 0)
          placement.compindex = value_or(float_position[8], 0)
          placement.screen_row = value_or(float_position[9], 0)
          placement.screen_col = value_or(float_position[10], 0)
        end
        placements[tostring(grid_id)] = placement
      end
    end
  end

  local msg
  if present(screen.msg_grid) then
    msg = {}
    msg.grid = screen.msg_grid
    msg.row = value_or(screen.msg_grid_pos, 0)
    msg.scrolled = value_or(screen.msg_scrolled, false)
    msg.sep_char = value_or(screen.msg_sep_char, '')
    msg.zindex = value_or(screen.msg_zindex, 0)
    msg.compindex = value_or(screen.msg_compindex, 0)
  else
    msg = vim.NIL
  end

  local cursor_source = screen._cursor or {}
  local cursor = {}
  cursor.grid = value_or(cursor_source.grid, 1)
  cursor.row = value_or(cursor_source.row, 1) - 1
  cursor.col = value_or(cursor_source.col, 1) - 1

  local canonical = {}
  canonical.v = 1
  canonical.default_colors = default_colors
  canonical.attrs = attrs
  canonical.grids = grids
  canonical.placements = placements
  canonical.msg = msg
  canonical.cursor = cursor
  canonical.mode = value_or(screen.mode, 'normal')
  local grid_pairs = {}
  for index, grid_id in ipairs(sorted_keys(grids)) do
    local grid_value = grids[grid_id]
    grid_pairs[index] = { grid_id, encode_object(grid_value, { 'w', 'h', 'rows' }) }
  end
  local placement_pairs = {}
  for index, grid_id in ipairs(sorted_keys(placements)) do
    local placement = placements[grid_id]
    local placement_keys
    if placement.kind == 'win' then
      placement_keys = { 'kind', 'win', 'row', 'col', 'width', 'height' }
    elseif placement.kind == 'float' then
      placement_keys = {
        'kind', 'win', 'anchor', 'anchor_grid', 'anchor_row', 'anchor_col',
        'mouse', 'zindex', 'compindex', 'screen_row', 'screen_col',
      }
    else
      placement_keys = { 'kind' }
    end
    placement_pairs[index] = { grid_id, encode_object(placement, placement_keys) }
  end
  local message_json = 'null'
  if msg ~= vim.NIL and msg ~= nil then
    message_json = encode_object(msg, { 'grid', 'row', 'scrolled', 'sep_char', 'zindex', 'compindex' })
  end
  return encode_pairs({
    { 'v', json_scalar(canonical.v) },
    { 'default_colors', encode_object(default_colors, { 'fg', 'bg', 'sp' }) },
    { 'attrs', '[' .. table.concat(canonical.attrs, ',') .. ']' },
    { 'grids', encode_pairs(grid_pairs) },
    { 'placements', encode_pairs(placement_pairs) },
    { 'msg', message_json },
    { 'cursor', encode_object(cursor, { 'grid', 'row', 'col' }) },
    { 'mode', json_scalar(canonical.mode) },
  })
end

return exporter
