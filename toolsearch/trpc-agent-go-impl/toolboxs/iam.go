//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolboxs

import (
	"trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// --- IAM tool input types ---

type createUserInput struct {
	Username    string `json:"username" jsonschema:"description=Unique username for the new user account, used for login"`
	Email       string `json:"email" jsonschema:"description=Email address associated with the user account"`
	DisplayName string `json:"display_name,omitempty" jsonschema:"description=Human-readable display name shown in the UI, defaults to username"`
}

type deleteUserInput struct {
	UserID string `json:"user_id" jsonschema:"description=Unique identifier of the user account to permanently delete"`
}

type listUsersInput struct {
	Filter   string `json:"filter,omitempty" jsonschema:"description=Optional filter string to search by username, email, or display name substring"`
	Page     int    `json:"page,omitempty" jsonschema:"description=Page number for paginated results, starting from 1, defaults to 1"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"description=Number of results per page, defaults to 20, maximum 100"`
}

type updateUserInput struct {
	UserID      string `json:"user_id" jsonschema:"description=Unique identifier of the user account to update"`
	Email       string `json:"email,omitempty" jsonschema:"description=New email address for the user"`
	DisplayName string `json:"display_name,omitempty" jsonschema:"description=New display name for the user"`
}

type getUserInput struct {
	UserID string `json:"user_id" jsonschema:"description=Unique identifier of the user account to retrieve"`
}

type grantRoleInput struct {
	UserID string `json:"user_id" jsonschema:"description=Unique identifier of the user account to grant the role to"`
	Role   string `json:"role" jsonschema:"description=Role name to assign, e.g. 'admin', 'editor', 'viewer'"`
}

type revokeRoleInput struct {
	UserID string `json:"user_id" jsonschema:"description=Unique identifier of the user account to revoke the role from"`
	Role   string `json:"role" jsonschema:"description=Role name to remove, e.g. 'admin', 'editor', 'viewer'"`
}

// IamToolbox returns the iam deferred-tool namespace.
func IamToolbox() toolsearch.Toolbox {
	return toolsearch.Toolbox{
		Name:        "iam",
		Description: "Identity and access management: manage user accounts, roles, and permissions. Supports creating, updating, deleting, listing and retrieving user profiles, as well as granting and revoking access roles.",
		Tools: []tool.Tool{
			stubTool[createUserInput]("create_user",
				"Create a new user account in the identity system. Requires a unique username and valid email address. Optionally set a display name. Returns the generated user ID."),
			stubTool[deleteUserInput]("delete_user",
				"Permanently delete a user account from the identity system by user ID. This operation cannot be undone. All associated roles and permissions are also removed."),
			stubTool[listUsersInput]("list_users",
				"List all user accounts in the identity system with optional filtering and pagination. Returns user ID, username, email, display name, and assigned roles for each user."),
			stubTool[updateUserInput]("update_user",
				"Update properties of an existing user account by user ID. Supports changing email address and display name. Fields left empty remain unchanged."),
			stubTool[getUserInput]("get_user",
				"Get details of a specific user account by user ID. Returns username, email, display name, assigned roles, account creation time, and last login time."),
			stubTool[grantRoleInput]("grant_role",
				"Grant a specific role to a user account. The role determines the user's access permissions within the system. Common roles: 'admin', 'editor', 'viewer'."),
			stubTool[revokeRoleInput]("revoke_role",
				"Revoke a specific role from a user account. The user will lose all permissions associated with that role. Does not affect other roles the user may have."),
		},
	}
}
