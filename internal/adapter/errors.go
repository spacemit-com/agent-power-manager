package adapter

import "errors"

var (
	ErrAgentNotInstalled  = errors.New("agent is not installed")
	ErrUnsupportedAdapter = errors.New("unsupported adapter")
	ErrTargetConflict     = errors.New("target path already exists")
	ErrInvalidSkill       = errors.New("invalid skill")
)
