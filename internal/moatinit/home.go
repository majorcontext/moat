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
