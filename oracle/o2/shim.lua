local script_source = debug.getinfo(1, 'S').source
local script_path = script_source:sub(1, 1) == '@' and script_source:sub(2) or script_source
local o2_dir = vim.fs.dirname(vim.fs.abspath(script_path))
local oracle_dir = vim.fs.dirname(o2_dir)
package.path = vim.fs.joinpath(oracle_dir, 'neovim-test', '?.lua') .. ';' .. package.path

local function shallowcopy(o) local r = {} for k, v in pairs(o) do r[k] = v end return r end
package.preload['test.testutil'] = function()
  return {
    shallowcopy = shallowcopy,
    concat_tables = function(...) local r = {} for _, tb in ipairs({...}) do for _, v in ipairs(tb) do r[#r+1] = v end end return r end,
    eq = function(a, b) assert(vim.deep_equal(a, b)) end,
    dedent = function(s) return s end,
  }
end
package.preload['test.functional.testnvim'] = function()
  return function()
    return {
      api = setmetatable({}, { __index = function(_, k) return function(...) local ok, rv = _G.SESSION:request(k, ...) assert(ok, vim.inspect(rv)) return rv end end }),
      run_session = function() error('not used') end,
      get_session = function() return _G.SESSION end,
      create_callindex = function(fn) return setmetatable({}, { __index = function(_, k) return function(...) return fn(k, ...) end end }) end,
    }
  end
end
package.preload['coxpcall'] = function() return { pcall = pcall } end
