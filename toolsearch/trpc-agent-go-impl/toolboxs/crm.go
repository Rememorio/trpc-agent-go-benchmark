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

// --- CRM tool input types ---

type createCustomerInput struct {
	Name     string `json:"name" jsonschema:"description=Full company or individual name of the new customer"`
	Email    string `json:"email,omitempty" jsonschema:"description=Primary contact email address for the customer"`
	Phone    string `json:"phone,omitempty" jsonschema:"description=Primary contact phone number for the customer"`
	Industry string `json:"industry,omitempty" jsonschema:"description=Industry vertical the customer belongs to, e.g. 'Technology', 'Healthcare', 'Finance'"`
}

type deleteCustomerInput struct {
	CustomerID string `json:"customer_id" jsonschema:"description=Unique identifier of the customer record to permanently delete"`
}

type listCustomersInput struct {
	Filter   string `json:"filter,omitempty" jsonschema:"description=Optional filter string to search by customer name, email, or industry substring"`
	Page     int    `json:"page,omitempty" jsonschema:"description=Page number for paginated results, starting from 1, defaults to 1"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"description=Number of results per page, defaults to 20, maximum 100"`
}

type updateCustomerInput struct {
	CustomerID string `json:"customer_id" jsonschema:"description=Unique identifier of the customer record to update"`
	Name       string `json:"name,omitempty" jsonschema:"description=New company or individual name for the customer"`
	Email      string `json:"email,omitempty" jsonschema:"description=New primary contact email address"`
	Phone      string `json:"phone,omitempty" jsonschema:"description=New primary contact phone number"`
	Industry   string `json:"industry,omitempty" jsonschema:"description=New industry vertical classification"`
}

type getCustomerInput struct {
	CustomerID string `json:"customer_id" jsonschema:"description=Unique identifier of the customer record to retrieve"`
}

type addContactInput struct {
	CustomerID string `json:"customer_id" jsonschema:"description=Unique identifier of the customer record to add a contact to"`
	Name       string `json:"name" jsonschema:"description=Full name of the contact person"`
	Email      string `json:"email,omitempty" jsonschema:"description=Email address of the contact person"`
	Phone      string `json:"phone,omitempty" jsonschema:"description=Phone number of the contact person"`
	Title      string `json:"title,omitempty" jsonschema:"description=Job title or role of the contact person, e.g. 'CTO' or 'Purchasing Manager'"`
}

type removeContactInput struct {
	CustomerID string `json:"customer_id" jsonschema:"description=Unique identifier of the customer record to remove a contact from"`
	ContactID  string `json:"contact_id" jsonschema:"description=Unique identifier of the contact person to remove"`
}

// CrmToolbox returns the crm deferred-tool namespace.
func CrmToolbox() toolsearch.Toolbox {
	return toolsearch.Toolbox{
		Name:        "crm",
		Description: "Customer relationship management: manage customer records, contacts, and sales leads. Supports creating, updating, deleting, listing and retrieving customers, as well as adding and removing contact persons for each customer.",
		Tools: []tool.Tool{
			stubTool[createCustomerInput]("create_customer",
				"Create a new customer record in the CRM system. Requires a customer name; email, phone, and industry are optional. Returns the generated customer ID."),
			stubTool[deleteCustomerInput]("delete_customer",
				"Permanently delete a customer record from the CRM system by customer ID. This operation cannot be undone. All associated contacts are also removed."),
			stubTool[listCustomersInput]("list_customers",
				"List all customer records in the CRM system with optional filtering and pagination. Returns customer ID, name, email, phone, industry, and associated contacts count."),
			stubTool[updateCustomerInput]("update_customer",
				"Update properties of an existing customer record by customer ID. Supports changing name, email, phone, and industry. Fields left empty remain unchanged."),
			stubTool[getCustomerInput]("get_customer",
				"Get full details of a specific customer record by customer ID. Returns name, email, phone, industry, creation date, last modified date, and list of associated contacts."),
			stubTool[addContactInput]("add_contact",
				"Add a new contact person to a customer record. Each contact has a name and optional email, phone, and job title. Returns the generated contact ID."),
			stubTool[removeContactInput]("remove_contact",
				"Remove a specific contact person from a customer record by contact ID. The customer record itself is not affected."),
		},
	}
}
