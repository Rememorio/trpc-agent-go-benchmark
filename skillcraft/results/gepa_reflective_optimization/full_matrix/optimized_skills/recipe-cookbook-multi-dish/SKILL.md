---
name: Recipe Cookbook - Multi-Dish
description: Collects meal search results, category listings, area/cuisine listings, and meal details for N dishes via meal API endpoints, then compiles results into a structured JSON cookbook with ingredient counts, difficulty ratings, cooking time estimates, and a cross-dish summary.
---

# Recipe Cookbook - Multi-Dish

## When to use

When the task requires creating a recipe cookbook or collecting multi-dimensional meal data (search, by category, by area, detailed ingredient info) for one or more dishes and saving the compiled result as a JSON file.

## Steps

1. If a workspace contains a cookbook_plan.json or similar helper file, read it first to understand any additional collection parameters (dish list, cuisines, categories, difficulty scales) that may refine the output. The task specification overrides the helper file when they disagree (e.g. exact dish count).
2. For each target dish, call `local-meal_search` with the dish's search name to find matching meals. Capture the meal_id, name, category, and area from the first matching result.
3. For each matched meal, call `local-meal_details` with meal_id to get full recipe data including ingredients, instructions, and nutrition estimates.
4. For each dish's category (derived from search results, e.g. Pasta, Chicken, Seafood, Beef), call `local-meal_by_category` to list meals in that category. Record total count and top 3 meals.
5. For each dish's cuisine area, call `local-meal_by_area` to list meals by cuisine. Record total count and top 3 meals. IMPORTANT: Use the area name as returned by the search/details API (e.g. 'Italy' may appear as 'Italian', 'India' as 'India', 'France' as 'France'). If an area query returns 0 meals, retry with the country-name form (e.g. 'Indian' → 'India', 'French' → 'France') since the API uses country names as area identifiers.
6. Calculate difficulty_rating (Easy/Medium/Hard based on ingredient count and instruction complexity) and estimated_cooking_time_min for each dish based on instruction steps and ingredient count.
7. Compile all results into a JSON file with a 'recipes' array (each containing name, meal_id, category, cuisine, ingredient_count, difficulty_rating, estimated_cooking_time_min, related_in_category, related_in_area) and a 'summary' object (total_recipes, cuisines_covered, categories_covered, difficulty_breakdown, avg_ingredient_count, total_cooking_time_min).
8. Save the compiled JSON to the required output file (e.g. recipe_cookbook.json) using the write tool, then verify by reading back the file header.
9. Call claim_done to mark task completion.

## Pitfalls

- The meal API uses country names (e.g. 'India', 'France') as area identifiers, NOT adjective forms ('Indian', 'French'). If `local-meal_by_area` returns 0 meals, retry with the country-name form. The search results may show area as 'India' or 'Italy' — use that exact value for the by_area call.
- Some searches return multiple matches (e.g. 'tandoori' returns both 'Tandoori chicken' and 'Tendir Choreyi'). Pick the match whose name best aligns with the target dish.
- The helper file (cookbook_plan.json) may list more dishes than the task specification requires. Always follow the task specification's exact dish list and count.
- Each recipe object must include `category_dishes` and `cuisine_dishes` keys (populated from the corresponding by_category and by_area tool results — count and top 3). Nested or renamed equivalents like `related_in_category`/`related_in_area` do not satisfy the artifact contract.
- Only call the endpoints explicitly listed in the task's tool list. If a task declares search, by_category, and by_area but not meal_details, do not call meal_details.
- Avoid repeating tool calls with identical argument sets. Cache or reuse the first result instead of re-invoking the same endpoint with the same parameters.
- Keep summaries compact — record total count and top 3 meals for category and area listings, not full result sets, to avoid excess token usage.
