package wezterm

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	luaOwnerMarker = "sshpic:wezterm:lua:v1"
	configBegin    = "-- >>> sshpic wezterm integration v1 (managed; use `sshpic restore wezterm`)"
	configEnd      = "-- <<< sshpic wezterm integration v1"
)

// LuaOptions controls the generated sshpic-owned WezTerm module.
type LuaOptions struct {
	BinaryPath      string
	DispatchCommand string
	PollInterval    time.Duration
	Timeout         time.Duration
}

// LuaIntegrationSource generates an asynchronous Ctrl+V integration. It sends
// LocalProcessInfo as JSON in a single argv element, polls an atomic result file,
// and never reads or retypes clipboard text.
func LuaIntegrationSource(opts LuaOptions) (string, error) {
	binary := strings.TrimSpace(opts.BinaryPath)
	if binary == "" {
		return "", errors.New("sshpic binary path is required")
	}
	command := strings.TrimSpace(opts.DispatchCommand)
	if command == "" {
		command = "wezterm-dispatch"
	}
	poll := opts.PollInterval
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if poll < 25*time.Millisecond || poll > time.Second {
		return "", errors.New("Lua poll interval must be between 25ms and 1s")
	}
	if timeout < time.Second || timeout > 2*time.Minute {
		return "", errors.New("Lua timeout must be between 1s and 2m")
	}
	maxPolls := int(timeout / poll)
	if maxPolls < 1 {
		maxPolls = 1
	}

	source := fmt.Sprintf(`-- %s
-- This file is owned by sshpic. Use "sshpic restore wezterm" to remove it.
local wezterm = require 'wezterm'
local module = {}

local sshpic_binary = %s
local dispatch_command = %s
local poll_seconds = %.3f
local max_polls = %d
local forbidden_path_bytes = %s
local in_flight = {}
local sequence = 0
local module_nonce = string.gsub(tostring({}), '[^%%w]', '')

local function basename(value)
  return string.lower(string.gsub(value or '', '(.*[/\\])(.*)', '%%2'))
end

local function is_focused_ssh(info)
  if type(info) ~= 'table' or type(info.executable) ~= 'string' then
    return false
  end
  local executable = basename(info.executable)
  if executable ~= 'ssh' and executable ~= 'ssh.exe' then
    return false
  end
  if type(info.argv) ~= 'table' or type(info.argv[1]) ~= 'string' then
    return false
  end
  local argv0 = basename(info.argv[1])
  return argv0 == 'ssh' or argv0 == 'ssh.exe'
end

local function original_pane_is_active(win, pane, pane_id)
  local ok, active = pcall(function() return win:active_pane() end)
  if not ok or active == nil then
    return false
  end
  local active_ok, active_id = pcall(function() return tostring(active:pane_id()) end)
  local pane_ok, current_id = pcall(function() return tostring(pane:pane_id()) end)
  return active_ok and pane_ok and active_id == pane_id and current_id == pane_id
end

local function same_argv(left, right)
  if type(left) ~= 'table' or type(right) ~= 'table' or #left ~= #right then
    return false
  end
  for index = 1, #left do
    if left[index] ~= right[index] then
      return false
    end
  end
  return true
end

local function normalize_executable(value)
  return string.lower(string.gsub(value or '', '/', '\\'))
end

local function original_process_is_current(pane, original)
  local ok, current = pcall(function() return pane:get_foreground_process_info() end)
  if not ok or type(current) ~= 'table' then
    return false
  end
  return tostring(current.pid or '') == tostring(original.pid or '')
      and normalize_executable(current.executable) == normalize_executable(original.executable)
      and same_argv(current.argv, original.argv)
end

local function delayed_target_is_current(win, pane, pane_id, original)
  return original_pane_is_active(win, pane, pane_id)
      and original_process_is_current(pane, original)
end

local function native_paste(win, pane, pane_id, original)
  if original == nil and original_pane_is_active(win, pane, pane_id) then
    win:perform_action(wezterm.action.PasteFrom 'Clipboard', pane)
    return
  end
  if original ~= nil and delayed_target_is_current(win, pane, pane_id, original) then
    win:perform_action(wezterm.action.PasteFrom 'Clipboard', pane)
    return
  end
  wezterm.log_warn('sshpic: focused pane or process changed; delayed paste was discarded')
end

local function notify_failure(win, result)
  if type(result) == 'table'
      and (result.kind == 'non_image' or result.kind == 'no_focused_target') then
    return
  end
  local reason = type(result) == 'table' and result.reason or nil
  if type(reason) ~= 'string' or reason == '' then
    return
  end
  wezterm.log_warn('sshpic: ' .. reason)
  pcall(function()
    win:toast_notification('sshpic', reason, nil, 4000)
  end)
end

local function safe_remote_path(value)
  if type(value) ~= 'string' or value == '' or string.sub(value, 1, 1) ~= '/' then
    return false
  end
  for index = 1, #value do
    local byte = string.byte(value, index)
    if byte < 32 or byte == 127 then
      return false
    end
    local character = string.char(byte)
    if string.find(forbidden_path_bytes, character, 1, true) then
      return false
    end
  end
  return true
end

local function temp_result_path(pane_id)
  sequence = sequence + 1
  local temp = os.getenv('TEMP') or os.getenv('TMP') or '.'
  local separator = package.config:sub(1, 1)
  if string.sub(temp, -1) ~= '/' and string.sub(temp, -1) ~= '\\' then
    temp = temp .. separator
  end
  return temp .. string.format('sshpic-wezterm-%%s-%%s-%%d-%%d.json', module_nonce, pane_id, os.time(), sequence)
end

local function read_result(path)
  local file = io.open(path, 'rb')
  if not file then
    return nil
  end
  local data = file:read('*a')
  file:close()
  if not data or data == '' then
    return nil
  end
  local ok, result = pcall(wezterm.json_parse, data)
  if not ok or type(result) ~= 'table' then
    return { action = 'error', kind = 'invalid_result', reason = 'sshpic returned invalid JSON' }
  end
  return result
end

local function start_dispatch(win, pane, info)
  local pane_id = tostring(pane:pane_id())
  if in_flight[pane_id] then
    wezterm.log_warn('sshpic: paste already in flight for pane ' .. pane_id)
    return
  end

  local original_argv = {}
  for index, value in ipairs(info.argv) do
    original_argv[index] = value
  end
  local process = {
    executable = info.executable,
    argv = original_argv,
    pid = info.pid,
  }
  local encoded_ok, process_json = pcall(wezterm.json_encode, process)
  if not encoded_ok then
    native_paste(win, pane, pane_id, process)
    return
  end

  local result_path = temp_result_path(pane_id)
  os.remove(result_path)
  in_flight[pane_id] = result_path
  local args = {
    sshpic_binary,
    dispatch_command,
    '--process-json', process_json,
    '--pane-id', pane_id,
    '--result-file', result_path,
  }

  local spawned, spawn_error = pcall(wezterm.background_child_process, args)
  if not spawned then
    in_flight[pane_id] = nil
    wezterm.log_error('sshpic: could not start dispatch: ' .. tostring(spawn_error))
    native_paste(win, pane, pane_id, process)
    return
  end

  local function poll(attempt)
    if in_flight[pane_id] ~= result_path then
      return
    end
    local result = read_result(result_path)
    if result then
      in_flight[pane_id] = nil
      os.remove(result_path)
      if (result.action == 'insert_local_image_path' or result.action == 'insert_remote_image_path')
          and type(result.payload) == 'string' and result.payload ~= '' then
        if result.action == 'insert_remote_image_path' and not safe_remote_path(result.payload) then
          local unsafe = { kind = 'unsafe_remote_path', reason = 'sshpic refused an unsafe remote image path' }
          notify_failure(win, unsafe)
          native_paste(win, pane, pane_id, process)
          return
        end
        if delayed_target_is_current(win, pane, pane_id, process) then
          pane:send_paste(result.payload)
        else
          wezterm.log_warn('sshpic: focused pane or process changed; image path insertion was discarded')
        end
        return
      end
      notify_failure(win, result)
      native_paste(win, pane, pane_id, process)
      return
    end
    if attempt >= max_polls then
      in_flight[pane_id] = nil
      os.remove(result_path)
      local result = { kind = 'timeout', reason = 'sshpic paste dispatch timed out' }
      notify_failure(win, result)
      native_paste(win, pane, pane_id, process)
      return
    end
    wezterm.time.call_after(poll_seconds, function() poll(attempt + 1) end)
  end

  wezterm.time.call_after(poll_seconds, function() poll(1) end)
end

function module.apply_to_config(config)
  config.keys = config.keys or {}
  table.insert(config.keys, {
    key = 'v',
    mods = 'CTRL',
    action = wezterm.action_callback(function(win, pane)
      local ok, info = pcall(function() return pane:get_foreground_process_info() end)
      if not ok or not is_focused_ssh(info) then
        local pane_id = tostring(pane:pane_id())
        native_paste(win, pane, pane_id)
        return
      end
      start_dispatch(win, pane, info)
    end),
  })
end

return module
`, luaOwnerMarker, luaQuote(binary), luaQuote(command), poll.Seconds(), maxPolls, luaQuote(shortcutForbiddenPathASCII))
	return source, nil
}

func luaQuote(value string) string { return strconv.Quote(value) }

func configBlock(modulePath, configIdentifier string) string {
	return configBegin + "\n" +
		"local _sshpic_wezterm_integration_v1 = dofile(" + luaQuote(modulePath) + ")\n" +
		"_sshpic_wezterm_integration_v1.apply_to_config(" + configIdentifier + ")\n" +
		configEnd + "\n"
}
