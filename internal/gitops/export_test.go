package gitops

// SetAfterPullHook installs a test hook invoked inside CommitAndPush /
// DeleteAndPush between the pre-operation pull and the worktree mutation.
// Tests call it to force a rejected-push by injecting a competing commit.
// Returns a function that restores the previous hook.
func SetAfterPullHook(fn func(attempt int)) func() {
	prev := afterPullHook
	afterPullHook = fn
	return func() { afterPullHook = prev }
}
