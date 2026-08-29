package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetGroups(c *gin.Context) {
	groupNames := make([]string, 0)
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		groupNames = append(groupNames, groupName)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    groupNames,
	})
}

func GetUserGroups(c *gin.Context) {
	usableGroups := make(map[string]map[string]interface{})
	userGroup := ""
	userId := c.GetInt("id")
	userGroup, _ = model.GetUserGroup(userId, false)
	userUsableGroups := service.GetUserUsableGroups(userGroup)
	for groupName, _ := range ratio_setting.GetGroupRatioCopy() {
		// UserUsableGroups contains the groups that the user can use
		if desc, ok := userUsableGroups[groupName]; ok {
			ratio := service.GetUserGroupRatio(userGroup, groupName)
			if userRatio, configured, err := model.GetGroupUserRatio(userId, groupName); err != nil {
				common.SysError("failed to load user-specific group ratio: " + err.Error())
			} else if configured {
				ratio = userRatio
			}
			usableGroups[groupName] = map[string]interface{}{
				"ratio": ratio,
				"desc":  desc,
			}
		}
	}
	if _, ok := userUsableGroups["auto"]; ok {
		usableGroups["auto"] = map[string]interface{}{
			"ratio": "自动",
			"desc":  setting.GetUsableGroupDescription("auto"),
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    usableGroups,
	})
}

type groupUserRatioRequest struct {
	UserID int     `json:"user_id"`
	Ratio  float64 `json:"ratio"`
}

func validateGroupUserRatioGroup(c *gin.Context) (string, bool) {
	group := strings.TrimSpace(c.Param("group"))
	if group == "" || !ratio_setting.ContainsGroupRatio(group) {
		common.ApiErrorMsg(c, "billing group does not exist")
		return "", false
	}
	return group, true
}

// GetGroupUserRatios lists user-specific ratios for a billing group.
func GetGroupUserRatios(c *gin.Context) {
	group, ok := validateGroupUserRatioGroup(c)
	if !ok {
		return
	}
	entries, err := model.ListGroupUserRatios(group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, entries)
}

// UpsertGroupUserRatio creates or updates one user's ratio in a billing group.
func UpsertGroupUserRatio(c *gin.Context) {
	group, ok := validateGroupUserRatioGroup(c)
	if !ok {
		return
	}
	var req groupUserRatioRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	if req.UserID <= 0 {
		common.ApiErrorMsg(c, "user_id must be positive")
		return
	}
	if err := model.ValidateGroupUserRatio(req.Ratio); err != nil {
		common.ApiError(c, err)
		return
	}
	if _, err := model.GetUserById(req.UserID, false); err != nil {
		common.ApiErrorMsg(c, "user does not exist")
		return
	}
	entry, err := model.UpsertGroupUserRatio(req.UserID, group, req.Ratio)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, entry)
}

// DeleteGroupUserRatio removes one user's override from a billing group.
func DeleteGroupUserRatio(c *gin.Context) {
	group, ok := validateGroupUserRatioGroup(c)
	if !ok {
		return
	}
	userID, err := strconv.Atoi(c.Param("user_id"))
	if err != nil || userID <= 0 {
		common.ApiErrorMsg(c, "invalid user_id")
		return
	}
	if err := model.DeleteGroupUserRatio(group, userID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorMsg(c, "user-specific ratio does not exist")
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
