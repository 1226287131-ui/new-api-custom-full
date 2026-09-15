package controller

import (
	"maps"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func pricingUserGroup(c *gin.Context) string {
	userID, exists := c.Get("id")
	if !exists {
		return ""
	}
	id, ok := userID.(int)
	if !ok || id <= 0 {
		common.SysError("invalid user id in pricing context")
		return ""
	}
	user, err := model.GetUserCache(id)
	if err != nil {
		common.SysError("failed to load user for pricing: " + err.Error())
		return ""
	}
	return user.Group
}

func filterPricingByUsableGroups(pricing []model.Pricing, usableGroup map[string]string) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		if common.StringsContains(item.EnableGroup, "all") {
			filtered = append(filtered, item)
			continue
		}
		for _, group := range item.EnableGroup {
			if _, ok := usableGroup[group]; ok {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

func GetPricing(c *gin.Context) {
	pricing := model.GetPricing()
	userId, exists := c.Get("id")
	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	maps.Copy(groupRatio, ratio_setting.GetGroupRatioCopy())
	var group string
	if exists {
		id, ok := userId.(int)
		if !ok || id <= 0 {
			common.SysError("invalid user id in pricing context")
		} else if user, err := model.GetUserCache(id); err == nil {
			group = user.Group
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
			}
			// A user-specific rule has the highest priority and must also be
			// reflected in the model plaza prices shown to this user.
			for g := range groupRatio {
				if ratio, configured, ratioErr := model.GetGroupUserRatio(id, g); ratioErr != nil {
					common.SysError("failed to load user-specific group ratio for pricing: " + ratioErr.Error())
				} else if configured {
					groupRatio[g] = ratio
				}
			}
		} else {
			common.SysError("failed to load user for pricing: " + err.Error())
		}
	}

	usableGroup = service.GetUserUsableGroups(group)
	pricing = filterPricingByUsableGroups(pricing, usableGroup)
	// check groupRatio contains usableGroup
	for group := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroup[group]; !ok {
			delete(groupRatio, group)
		}
	}

	c.JSON(200, gin.H{
		"success":            true,
		"data":               pricing,
		"vendors":            model.GetVendors(),
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"supported_endpoint": model.GetSupportedEndpointMap(),
		"auto_groups":        service.GetUserAutoGroup(group),
		"pricing_version":    "a42d372ccf0b5dd13ecf71203521f9d2",
	})
}

func GetPricingTaskSuccessRates(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	usableGroups := service.GetUserUsableGroups(pricingUserGroup(c))
	pricing := filterPricingByUsableGroups(model.GetPricing(), usableGroups)
	snapshot, err := model.GetTaskSuccessRates(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load task success rates"})
		return
	}
	type taskSuccessRate struct {
		model.TaskOutcomeCounts
		SuccessRate *float64 `json:"success_rate"`
	}
	rates := make(map[string]taskSuccessRate)
	for _, item := range pricing {
		counts := model.TaskOutcomeCounts{}
		for group, value := range snapshot.Models[item.ModelName] {
			if _, allowed := usableGroups[group]; !allowed {
				continue
			}
			if !common.StringsContains(item.EnableGroup, "all") && !common.StringsContains(item.EnableGroup, group) {
				continue
			}
			counts.Total += value.Total
			counts.Success += value.Success
			counts.Failure += value.Failure
			counts.Pending += value.Pending
		}
		if !item.IsTaskModel && counts.Total == 0 {
			continue
		}
		rate := taskSuccessRate{TaskOutcomeCounts: counts}
		if finished := counts.Success + counts.Failure; finished > 0 {
			percentage := float64(counts.Success) / float64(finished) * 100
			rate.SuccessRate = &percentage
		}
		rates[item.ModelName] = rate
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"window_start": snapshot.WindowStart,
		"window_end":   snapshot.WindowEnd,
		"models":       rates,
	}})
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	err := model.UpdateOption("ModelRatio", defaultStr)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	err = ratio_setting.UpdateModelRatioByJSONString(defaultStr)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "重置模型倍率成功",
	})
}
