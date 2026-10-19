package runner

// LifecycleEnv layers the environment of exec, lifecycle commands and the
// dotfiles install in upstream's order: probed shell environment, caller
// remote environment (--remote-env), configuration remoteEnv, then secrets
// (--secrets-file). A later layer wins for the same name. Lifecycle callers
// keep the result in Runtime.Env only, so secrets reach exec environments
// without being stored.
func LifecycleEnv(probed, remote, config, secrets map[string]string) map[string]string {
	out := make(map[string]string, len(probed)+len(remote)+len(config)+len(secrets))
	for _, layer := range []map[string]string{probed, remote, config, secrets} {
		for k, v := range layer {
			out[k] = v
		}
	}
	return out
}
