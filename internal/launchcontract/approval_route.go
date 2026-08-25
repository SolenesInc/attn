package launchcontract

// The empty value is reserved for launch records written before this field was
// persisted; it must never be interpreted from a later global setting.
type ApprovalRoute string

const (
	ApprovalRouteUser     ApprovalRoute = "user"
	ApprovalRouteReviewer ApprovalRoute = "reviewer"
	ApprovalRouteBypass   ApprovalRoute = "bypass"
)

func (r ApprovalRoute) Valid() bool {
	switch r {
	case ApprovalRouteUser, ApprovalRouteReviewer, ApprovalRouteBypass:
		return true
	default:
		return false
	}
}

func (r ApprovalRoute) ReviewerInLoop() bool {
	return r == ApprovalRouteReviewer
}

// Call it after unattended policy has replaced the attended flags.
func ResolveApprovalRoute(yoloMode, autoApprove bool, unattended UnattendedLaunchSpec) ApprovalRoute {
	if !unattended.IsZero() {
		return ApprovalRouteReviewer
	}
	if yoloMode {
		return ApprovalRouteBypass
	}
	if autoApprove {
		return ApprovalRouteReviewer
	}
	return ApprovalRouteUser
}
