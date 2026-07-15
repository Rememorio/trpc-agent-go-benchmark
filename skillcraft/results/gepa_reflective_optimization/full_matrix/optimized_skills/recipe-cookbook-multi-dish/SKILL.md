---
name: Create Recipe Cookbook with 3 International Dishes
description: Create a recipe cookbook by collecting data for three specified international dishes using three API endpoints for searching meals by name, filtering meals by category, and filtering meals by ingredient. Additionally, calculate the difficulty rating and estimated cooking time for each dish.
---

# Create Recipe Cookbook with 3 International Dishes

## When to use

Use this skill when you need to compile a cookbook for multiple international dishes, gathering meal data from the domain APIs declared in the task and calculating difficulty and cooking time for each dish.

## Steps

1. Collect details for each of the three international dishes by searching for the meal by name.
2. List meals in the category corresponding to each dish.
3. List meals using a relevant ingredient for each dish.
4. Calculate the difficulty rating and cooking time for each dish.
5. Compile the results into a structured JSON format with required information such as name, category, cuisine, ingredient count, difficulty rating, estimated cooking time, total recipes, cuisines covered, and analysis date.
6. Save the final output to a specified JSON file.

## Pitfalls

- Ensure the API endpoints are accessible and functioning to avoid missing data.
- Accurately match dishes with their corresponding categories and ingredients to ensure correct data is fetched.
- Make sure the output format strictly adheres to the required JSON structure.
- Include the exact keys `category_dishes` and `cuisine_dishes` (or ingredient-based equivalent) on each recipe object, populated from the corresponding tool results; nested or renamed equivalents do not satisfy the artifact contract.
- Only call the domain tools the task explicitly declares; do not invoke endpoints not listed in the task's workflow.
- After writing the output file, always call the completion tool (e.g., `claim_done`) to signal task completion.
- Do not repeat tool calls with identical arguments; reuse the result from the first call instead of re-invoking the same endpoint for the same dish or query.
