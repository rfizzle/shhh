//go:build !linux && !windows

package sandbox

// pollHangupEvents is nothing beyond the hang-up itself where the platform
// has no half-close event to ask for.
const pollHangupEvents = 0
