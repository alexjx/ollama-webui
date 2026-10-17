package agent

import "fmt"

// WorkspaceInstructions describes the configured root without granting tools or
// implying that a file has been created. Callers supply the resolved runtime path.
func WorkspaceInstructions(workspace string) string {
	if workspace == "" {
		return ""
	}
	return fmt.Sprintf("\n\nWorkspace file references: The configured workspace root is %q. When referring to files or directories inside this workspace in your replies, use their full absolute paths beginning with this exact root, not relative paths, bare filenames, or the literal variable AGENT_WORKSPACE. Format each path as inline code, or use the absolute path as a Markdown link target, so the user can open it in the Workspace browser. Preserve spaces and Unicode in paths. Do not invent paths, imply that unverified files exist, or rewrite paths outside the workspace as workspace paths.", workspace)
}
