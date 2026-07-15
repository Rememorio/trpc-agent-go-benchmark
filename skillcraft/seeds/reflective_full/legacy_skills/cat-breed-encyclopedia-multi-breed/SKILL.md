---
name: Cat Breed Encyclopedia - Multi-Breed
description: Collects breed profile, country relatives, coat family, and curated facts for N cat breeds via four catfacts API endpoints per breed, then compiles results into a structured JSON encyclopedia with a cross-breed summary.
---

# Cat Breed Encyclopedia - Multi-Breed

## When to use

When the task requires creating a cat breed encyclopedia or collecting multi-dimensional breed data (profile, country relatives, coat family, facts collection) for one or more cat breeds and saving the compiled result as a JSON file.

## Steps

1. If a workspace contains a collections.json or similar helper file, read it first to understand any additional collection parameters (fact categories, max lengths, counts) that may refine the output.
2. For each target breed, call four API endpoints: (1) local-catfacts_breed_profile with breed_name, (2) local-catfacts_breed_relatives with breed_name, (3) local-catfacts_breed_coat_family with breed_name, (4) local-catfacts_breed_facts with breed_name.
3. All four calls for a breed can be batched in a single assistant turn since they are independent. Calls for multiple breeds can also be batched together for efficiency.
4. For each breed, extract the profile data (name, country, origin, coat, pattern, characteristics, uniqueness_score), country relatives (country, total breeds, rank, relatives list with similarity scores), coat family (coat type, total breeds in family, rank, countries represented, pattern variety), and facts collection (fact count, categories, sample facts).
5. Compile results into a JSON object with a 'breeds' array (each entry having breed_name, profile, country_relatives, coat_family, and facts) and a 'summary' object containing cross-breed statistics (total_breeds_analyzed, countries_covered, coat_type_distribution, uniqueness_scores, shared_coat_family_breeds, apis_used_per_breed, total_api_calls).
6. Include a 'generation_date' field at the top level of the output JSON.
7. Save the compiled JSON to the required output file (e.g., cat_encyclopedia.json) using the final-write tool.
8. Call claim_done to mark the task as complete.

## Pitfalls

- The breed_facts endpoint returns a large volume of facts (e.g., 50 facts per breed); summarize or sample rather than including all raw facts to keep output manageable.
- The breed_profile endpoint may return a different breed name than the input (e.g., querying 'Persian' returns 'Himalayan, or Colorpoint Persian'); use the input breed name as breed_name in the output but include the API-returned name in the profile.
- Multiple breeds may share the same coat family (e.g., Long coat includes Persian, Maine Coon, Ragdoll); capture shared_coat_family_breeds in the summary for cross-breed analysis.
- Ensure the summary's countries_covered list reflects the countries from the breed profiles, not just the task specification table.
