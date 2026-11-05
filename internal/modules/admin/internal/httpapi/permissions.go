package httpapi

import (
	"context"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// anyAdmin marks an operation every signed-in admin may call (or, for
// login, anyone: Scope lets it through without a session).
const anyAdmin domain.Permission = ""

// permissions is what each console operation needs (ADR-0077), by its
// handler's name. An
// operation missing here is refused for everyone, and
// TestEveryOperationHasAPermission fails, so none is forgotten.
var permissions = map[string]domain.Permission{ //nolint:gochecknoglobals // a fixed table
	"AdminLogin":  anyAdmin,
	"AdminLogout": anyAdmin,
	"AdminMe":     anyAdmin,
	// The home: every admin.
	"AdminHealth":     anyAdmin,
	"AdminGetMetrics": anyAdmin,

	"AdminListStores":           domain.PermStoresRead,
	"AdminGetStore":             domain.PermStoresRead,
	"AdminGetStoreProfile":      domain.PermStoresRead, // the owner's contact needs contacts.read too (app)
	"AdminGetStoreEntitlements": domain.PermStoresRead,
	"AdminSuspendStore":         domain.PermStoresManage,
	"AdminReactivateStore":      domain.PermStoresManage,
	"AdminImpersonateStore":     domain.PermStoresManage,
	"AdminSetStoreOverride":     domain.PermStoresManage,
	"AdminClearStoreOverride":   domain.PermStoresManage,

	"AdminGetStoreBilling":        domain.PermBillingRead,
	"AdminGetStoreBalance":        domain.PermBillingRead,
	"AdminGetCollectionSettings":  domain.PermBillingRead,
	"AdminListDuplicatePayments":  domain.PermBillingRead,
	"AdminBillingSummary":         domain.PermBillingRead,
	"AdminGetGrowth":              domain.PermBillingRead,
	"AdminListInvoices":           domain.PermBillingRead,
	"AdminGetInvoice":             domain.PermBillingRead,
	"AdminListPayments":           domain.PermBillingRead,
	"AdminSettleInvoice":          domain.PermBillingManage,
	"AdminCreditStoreBalance":     domain.PermBillingManage,
	"AdminSwitchAgencyStores":     domain.PermBillingManage,
	"AdminSaveCollectionSettings": domain.PermBillingManage,
	"AdminSettleAccountInvoice":   domain.PermBillingManage,
	"AdminMarkDuplicateRefunded":  domain.PermBillingManage,
	"AdminExportInvoices":         domain.PermBillingManage,

	"AdminListPlans":         domain.PermCatalogRead,
	"AdminListFeatures":      domain.PermCatalogRead,
	"AdminGetFounding":       domain.PermCatalogRead,
	"AdminListAgencies":      domain.PermCatalogRead,
	"AdminGetAgency":         domain.PermCatalogRead,
	"AdminPublishPlan":       domain.PermCatalogManage,
	"AdminRetirePlan":        domain.PermCatalogManage,
	"AdminSaveFeature":       domain.PermCatalogManage,
	"AdminSaveFounding":      domain.PermCatalogManage,
	"AdminCreateAgency":      domain.PermCatalogManage,
	"AdminAddAgencyStore":    domain.PermCatalogManage,
	"AdminRemoveAgencyStore": domain.PermCatalogManage,

	"AdminGetSite":               domain.PermContentRead,
	"AdminListSiteGuides":        domain.PermContentRead,
	"AdminGetSiteGuide":          domain.PermContentRead,
	"AdminPreviewSiteGuide":      domain.PermContentRead,
	"AdminGetAds":                domain.PermContentRead,
	"AdminSaveSiteSettings":      domain.PermContentManage,
	"AdminSaveSiteStory":         domain.PermContentManage,
	"AdminCreateSiteUpload":      domain.PermContentManage,
	"AdminSetSiteImage":          domain.PermContentManage,
	"AdminRemoveSiteImage":       domain.PermContentManage,
	"AdminCreateSiteFAQ":         domain.PermContentManage,
	"AdminUpdateSiteFAQ":         domain.PermContentManage,
	"AdminDeleteSiteFAQ":         domain.PermContentManage,
	"AdminCreateSitePerson":      domain.PermContentManage,
	"AdminUpdateSitePerson":      domain.PermContentManage,
	"AdminDeleteSitePerson":      domain.PermContentManage,
	"AdminCreateSiteTestimonial": domain.PermContentManage,
	"AdminUpdateSiteTestimonial": domain.PermContentManage,
	"AdminDeleteSiteTestimonial": domain.PermContentManage,
	"AdminCreateSiteGuide":       domain.PermContentManage,
	"AdminUpdateSiteGuide":       domain.PermContentManage,
	"AdminDeleteSiteGuide":       domain.PermContentManage,
	"AdminSaveAdSettings":        domain.PermContentManage,
	"AdminCreateAdUpload":        domain.PermContentManage,
	"AdminCreateAdBanner":        domain.PermContentManage,
	"AdminSetAdBannerActive":     domain.PermContentManage,

	"AdminListAdmins": domain.PermAuditRead,
	"AdminAuditLog":   domain.PermAuditRead,
	// Every admin searches; each kind is filtered by its own permission
	// inside (ADR-0079).
	"AdminSearch": anyAdmin,
	// Every admin reads the alerts its role hears of (filtered inside)
	// and chooses its own email (ADR-0080).
	"AdminListAlerts":    anyAdmin,
	"AdminSetAlertEmail": anyAdmin,
	// Each admin turns notifications on or off in its own devices.
	"AdminPushStatus":      anyAdmin,
	"AdminSubscribePush":   anyAdmin,
	"AdminUnsubscribePush": anyAdmin,

	"AdminGetTeam":      domain.PermTeamManage,
	"AdminInviteAdmin":  domain.PermTeamManage,
	"AdminRenewInvite":  domain.PermTeamManage,
	"AdminCancelInvite": domain.PermTeamManage,
	"AdminSetRole":      domain.PermTeamManage,
	"AdminDisableAdmin": domain.PermTeamManage,
	"AdminEnableAdmin":  domain.PermTeamManage,
	"AdminResetAdmin":   domain.PermTeamManage,
	// The invitee is not an admin yet: no session (Scope's public routes).
	"AdminLookupInvite": anyAdmin,
	"AdminAcceptInvite": anyAdmin,
}

var errForbidden = errs.Forbidden("your role does not allow this")

// authorize refuses an operation the signed-in admin's role does not
// grant, before its handler runs. Login carries no identity; every other
// route has one (Scope).
func authorize(f StrictHandlerFunc, operationID string) StrictHandlerFunc {
	need, known := permissions[operationID]
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		if !known {
			return nil, errForbidden
		}
		if need != anyAdmin {
			if who, ok := app.IdentityFrom(ctx); !ok || !who.Can(need) {
				return nil, errForbidden
			}
		}
		return f(ctx, w, r, request)
	}
}
