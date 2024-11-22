package controller

import (
	"context"
	"errors"
	"github.com/ente-io/museum/ente"
	bonus "github.com/ente-io/museum/ente/storagebonus"
	"github.com/ente-io/museum/pkg/controller/storagebonus"
	"github.com/ente-io/museum/pkg/controller/usercache"
	"github.com/ente-io/museum/pkg/repo"
	"github.com/ente-io/stacktrace"
)

// UsageController exposes functions which can be used to check around storage
type UsageController struct {
	BillingCtrl      *BillingController
	StorageBonusCtrl *storagebonus.Controller
	UserCacheCtrl    *usercache.Controller
	UsageRepo        *repo.UsageRepository
	UserRepo         *repo.UserRepository
	FamilyRepo       *repo.FamilyRepository
	FileRepo         *repo.FileRepository
}

const MaxLockerFiles = 10000

// CanUploadFile returns error if the file of given size (with StorageOverflowAboveSubscriptionLimit buffer) can be
// uploaded or not. If size is not passed, it validates if current usage is less than subscription storage.
func (c *UsageController) CanUploadFile(ctx context.Context, userID int64, size *int64, app ente.App) error {
	// If app is Locker, limit to MaxLockerFiles files
	if app == ente.Locker {
		// Get file count
		if fileCount, err := c.UserCacheCtrl.GetUserFileCountWithCache(userID, app); err != nil {
			if fileCount >= MaxLockerFiles {
				return stacktrace.Propagate(ente.ErrFileLimitReached, "")
			}
		}
	}

	familyAdminID, err := c.UserRepo.GetFamilyAdminID(userID)
	if err != nil {
		return stacktrace.Propagate(err, "")
	}
	var subscriptionAdminID int64
	var subscriptionUserIDs []int64
	// if user is part of a family group, validate if subscription of familyAdmin is valid & member's total storage
	// is less than the storage accordingly to subscription plan of the admin
	if familyAdminID != nil {
		familyMembers, err := c.FamilyRepo.GetMembersWithStatus(*familyAdminID, repo.ActiveFamilyMemberStatus)
		if err != nil {
			return stacktrace.Propagate(err, "failed to fetch family members")
		}
		subscriptionAdminID = *familyAdminID
		for _, familyMember := range familyMembers {
			subscriptionUserIDs = append(subscriptionUserIDs, familyMember.MemberUserID)
		}
	} else {
		subscriptionAdminID = userID
		subscriptionUserIDs = []int64{userID}
	}

	var subStorage int64
	var bonus *bonus.ActiveStorageBonus
	sub, err := c.BillingCtrl.GetActiveSubscription(subscriptionAdminID)
	if err != nil {
		subStorage = 0
		if errors.Is(err, ente.ErrNoActiveSubscription) {
			bonusRes, bonErr := c.UserCacheCtrl.GetActiveStorageBonus(ctx, subscriptionAdminID)
			if bonErr != nil {
				return stacktrace.Propagate(bonErr, "failed to get bonus data")
			}
			if bonusRes.GetMaxExpiry() <= 0 {
				return stacktrace.Propagate(err, "all bonus & plan expired")
			}
			bonus = bonusRes
		} else {
			return stacktrace.Propagate(err, "")
		}
	} else {
		subStorage = sub.Storage
	}
	usage, err := c.UsageRepo.GetCombinedUsage(ctx, subscriptionUserIDs)
	if err != nil {
		return stacktrace.Propagate(err, "")
	}
	newUsage := usage

	if size != nil {
		// Add the size of the file to be uploaded to the current usage and buffer in sub.Storage
		newUsage += *size
		subStorage += StorageOverflowAboveSubscriptionLimit
	}
	if newUsage > subStorage {
		if bonus == nil {
			// Check if the subAdmin has any storage bonus
			bonus, err = c.UserCacheCtrl.GetActiveStorageBonus(ctx, subscriptionAdminID)
			if err != nil {
				return stacktrace.Propagate(err, "failed to get storage bonus")
			}
		}
		var eligibleBonus = bonus.GetUsableBonus(subStorage)
		if newUsage > (subStorage + eligibleBonus) {
			return stacktrace.Propagate(ente.ErrStorageLimitExceeded, "")
		}
	}
	return nil
}
