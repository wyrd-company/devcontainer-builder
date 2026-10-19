package runner

// LifecycleEnv layers the environment of exec, lifecycle commands and the
// dotfiles install in upstream's order: caller remote environment (upstream
// --remote-env), then the configuration's remoteEnv, then secrets (upstream
// --secrets-file). A later layer wins for the same name. Lifecycle callers keep the
// result in Runtime.Env only, so that secrets reach exec environments and
// nothing that is stored.
func LifecycleEnv(remote, config, secrets map[string]string) map[string]string {
	out := make(map[string]string, len(remote)+len(config)+len(secrets))
	for _, layer := range []map[string]string{remote, config, secrets} {
		for k, v := range layer {
			out[k] = v
		}
	}
	return out
}
