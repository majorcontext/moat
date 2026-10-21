package moatinit

// targetHome is the repeated root-detection + home-selection idiom
// (X-TARGETHOME-IDIOM), shared by the agent-staging blocks, the init-files
// block, and the workspace .mcp.json / volume-chown guards:
//
//	if [ "$(id -u)" = "0" ] && id moatuser >/dev/null 2>&1; then
//	  TARGET_HOME="/home/moatuser"
//	else
//	  TARGET_HOME="$HOME"
//	fi
//
// When root with a moatuser account, staged files go under the hardcoded
// literal /home/moatuser — deliberately NOT moatuser's passwd home entry
// (parity: a custom base image whose moatuser home differs still stages to
// /home/moatuser, byte-for-byte like the script). Otherwise the current
// $HOME.
func targetHome(euid int, moatuserExists bool, home string) string {
	if euid == 0 && moatuserExists {
		return "/home/moatuser"
	}
	return home
}

// chownToMoatuser reports whether ownership fixups run (the same
// root+moatuser predicate; on any other branch no chown of any kind
// happens — files stay owned by the writing process).
func chownToMoatuser(euid int, moatuserExists bool) bool {
	return euid == 0 && moatuserExists
}

// initFilesOwnership mirrors the init-files ownership resolution (INIT-02):
// the ancestor-chown walk stops at initHome, which is /home/moatuser on the
// chown path and $HOME otherwise.
func initFilesOwnership(euid int, moatuserExists bool, home string) (chown bool, initHome string) {
	if euid == 0 && moatuserExists {
		return true, "/home/moatuser"
	}
	return false, home
}

// ResolveHome repairs HOME for a non-root entrypoint whose UID has no passwd
// entry in the image. On Linux bind mode the container runs as the workspace
// owner's UID, and when the image has no account for it Docker sets HOME to
// "/", where staging fails ("mkdir //.claude: permission denied"). If the
// image has /home/moatuser — the home the run's mounts and agent staging
// target — HOME is pointed there and exported, so the user command sees the
// same value.
//
// A HOME the UID's own passwd entry supplies is left alone: in images built
// FROM node:*, UID 1000 is the node user with HOME=/home/node, and overriding
// it would separate agent config from the account's home directory.
func ResolveHome(sys Sys) {
	if sys.Geteuid() == 0 || sys.Getenv("HOME") != "/" {
		return
	}
	if isDir(sys, "/home/moatuser") {
		sys.Setenv("HOME", "/home/moatuser")
	}
}
