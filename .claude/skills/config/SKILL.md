---
name: config
description: Guidelines for adding, removing, or updating configuration options in cursortab.nvim. Use when modifying config fields, enum values, or validation logic.
---

## Design Principle

**Lua owns all default values.** The Go daemon receives the complete config via the `CURSORTAB_CONFIG` environment variable with all defaults already applied. Go structs should not have default values or use pointer types for optional fields - all fields are required and must be provided by Lua.

## Files to Update

When modifying config options, update these locations:

### 1. Lua Side

**`lua/cursortab/config.lua`**
- Type annotation in `---@class` block (e.g., `---@field new_option type`)
- Default value in `default_config` table (required - Go expects all values)
- Validation (if enum-like, add to `valid_*` table and update error in `validate_config`)

### 2. Go Side

**`server/main.go`**
- Struct field with JSON tag in the appropriate config struct (`Config`, `ProviderConfig`, `BehaviorConfig`, etc.)
- No default values or optional fields - Lua provides the complete config
- Validation in `Config.Validate()` method (enum checks use local maps, numeric range checks)

**`server/logger/logger.go`** (for log levels only)
- `LogLevel` constants (`LogLevelTrace`, `LogLevelDebug`, etc.)
- `String()` method switch case
- `ParseLogLevel()` function switch case

### 3. Documentation

**`README.md`**
- Configuration example in the setup block
- Add comment showing valid values for enum options

**`doc/cursortab.txt`**
- Vim help file with same configuration example
- Keep in sync with README.md

## Checklist

For enum-like options (e.g., log_level, provider.type):

- [ ] Add to Lua `valid_*` table in config.lua (e.g., `valid_log_levels`, `valid_provider_types`)
- [ ] Update Lua error message in `validate_config()` with new valid values
- [ ] Add to Go validation map in `Config.Validate()` in main.go
- [ ] Update Go error message with new valid values
- [ ] Update README.md example/comments
- [ ] Update doc/cursortab.txt example/comments

For simple options:

- [ ] Add `---@field` type annotation in the appropriate `---@class` block in config.lua
- [ ] Add default value in `default_config` table in config.lua
- [ ] Add struct field with JSON tag in main.go (in `Config` or nested struct)
- [ ] Add validation in `Config.Validate()` if needed (numeric ranges, path validation, etc.)
- [ ] Update README.md example
- [ ] Update doc/cursortab.txt example

## Example: Adding a new enum value

When adding "trace" to log_level:

```lua
-- config.lua
local valid_log_levels = { trace = true, debug = true, info = true, warn = true, error = true }

-- In validate_config():
-- error: "Must be one of: trace, debug, info, warn, error"
```

```go
// main.go - inside Config.Validate()
validLogLevels := map[string]bool{"trace": true, "debug": true, "info": true, "warn": true, "error": true}
// error: "must be one of trace, debug, info, warn, error"
```

```go
// logger/logger.go - add constant, update String() and ParseLogLevel()
const LogLevelTrace LogLevel = iota
```

```markdown
<!-- README.md and doc/cursortab.txt -->
log_level = "info",  -- "trace", "debug", "info", "warn", "error"
```
