-- Buffer state management for cursortab.nvim

---@class BufferModule
local buffer = {}

---@class BufferState
---@field is_floating_window boolean
---@field is_modifiable boolean
---@field is_readonly boolean
---@field filetype string
---@field should_skip boolean Combined check result
---@field current_buf integer|nil
---@field current_win integer|nil

-- Filetypes to skip (set for O(1) lookup)
local skip_filetypes = { [""] = true, help = true, qf = true, netrw = true, fugitive = true, NvimTree = true }

-- Global buffer state cache to avoid expensive API calls on every cursor movement
---@type BufferState
local buffer_state = {
	is_floating_window = false,
	is_modifiable = false,
	is_readonly = false,
	filetype = "",
	should_skip = true, -- Combined check result
	current_buf = nil,
	current_win = nil,
}

-- Function to update buffer state (called when buffer/window changes)
local function update_buffer_state()
	---@type integer
	local current_buf = vim.api.nvim_get_current_buf()
	---@type integer
	local current_win = vim.api.nvim_get_current_win()

	-- Only update if buffer or window actually changed
	if buffer_state.current_buf == current_buf and buffer_state.current_win == current_win then
		return
	end

	-- Re-check if we're still in the same buffer/window after defer
	if vim.api.nvim_get_current_buf() ~= current_buf or vim.api.nvim_get_current_win() ~= current_win then
		return
	end

	-- Update cached state
	buffer_state.current_buf = current_buf
	buffer_state.current_win = current_win

	-- Check if in floating window
	---@type table
	local win_config = vim.api.nvim_win_get_config(current_win)
	buffer_state.is_floating_window = win_config.relative ~= ""

	-- Check buffer properties
	buffer_state.is_modifiable = vim.api.nvim_get_option_value("modifiable", { buf = current_buf })
	buffer_state.is_readonly = vim.api.nvim_get_option_value("readonly", { buf = current_buf })
	buffer_state.filetype = vim.api.nvim_get_option_value("filetype", { buf = current_buf })

	-- Combined check: should we skip idle completions for this buffer?
	local should_skip_filetype = skip_filetypes[buffer_state.filetype] or false
	buffer_state.should_skip = buffer_state.is_floating_window
		or not buffer_state.is_modifiable
		or buffer_state.is_readonly
		or should_skip_filetype
end

-- Public API

-- Update cached buffer state
function buffer.update_state()
	update_buffer_state()
end

-- Check if current buffer should be skipped
---@return boolean
function buffer.should_skip()
	return buffer_state.should_skip
end

return buffer
