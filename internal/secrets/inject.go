package secrets

import "sort"

// ExecEnvFlags builds the `-e NAME` PASS-THROUGH docker flags for run-time secret
// injection (SB2c's env channel). Crucially the flags carry the NAME only, never the
// value — the value must be placed in the docker process's OWN environment (ExecEnvKV),
// and docker forwards it into the container. This keeps the secret off the docker
// command line (world-readable /proc/<pid>/cmdline) and only in the process environment
// (owner-only /proc/<pid>/environ). Names are sorted for a stable order; a nil/empty map
// yields no flags.
func ExecEnvFlags(secretEnv map[string]string) []string {
	names := sortedNames(secretEnv)
	args := make([]string, 0, len(names)*2)
	for _, name := range names {
		args = append(args, "-e", name)
	}
	return args
}

// ExecEnvKV builds sorted NAME=value entries to place in the docker process's own
// environment (cmd.Env), the values the ExecEnvFlags `-e NAME` pass-through forwards
// into the container. These live only in the child process environment, never on its
// argv. A nil/empty map yields no entries.
func ExecEnvKV(secretEnv map[string]string) []string {
	names := sortedNames(secretEnv)
	kv := make([]string, 0, len(names))
	for _, name := range names {
		kv = append(kv, name+"="+secretEnv[name])
	}
	return kv
}

func sortedNames(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
