---
name: Pokemon Pokedex - Multi-Pokemon
description: Collects details, species, evolution, and moves for N Pokemon via four Pokemon API endpoints per Pokemon, then compiles results into a structured JSON file with cross-Pokemon summary statistics.
---

# Pokemon Pokedex - Multi-Pokemon

## When to use

When the task requires creating a Pokedex or collecting multi-dimensional Pokemon data (details, species, evolution, moves) for one or more Pokemon and saving the compiled result as a JSON file.

## Steps

1. If a workspace contains a collections.json or similar helper file, read it first to understand any additional collection parameters that may refine the output.
2. For each target Pokemon, call four API endpoints in the correct order: (1) local-pokemon_get_details with pokemon_id, (2) local-pokemon_get_species with pokemon_id, (3) local-pokemon_get_evolution with the evolution_chain_id extracted from the species response, (4) local-pokemon_get_moves with pokemon_id. Steps 1 and 2 can be parallelized across all Pokemon, but step 3 MUST wait until step 2 completes for that Pokemon because the evolution endpoint requires the evolution_chain_id from the species response.
3. From each get_details response, extract: id, name, types (as a list of type name strings), base stats (and compute stat_total by summing all base stat values).
4. From each get_species response, extract: genus, generation, is_legendary (legendary status), and evolution_chain_id.
5. From each get_evolution response, extract: evolution chain info (e.g., chain links, evolution stages).
6. From each get_moves response, extract: move list and total move count.
7. Calculate summary statistics across all Pokemon: total_pokemon count, avg_base_stat_total (average of stat_total across all Pokemon), and any other requested aggregate fields.
8. Compile all results into a JSON object with keys: 'pokemon' (array of per-Pokemon entry objects), 'summary' (object with total_pokemon and avg_base_stat_total), and 'analysis_date' (current date string).
9. Save the compiled JSON to the required output file (e.g., pokedex_entries.json) in the workspace directory. This step is mandatory — the task is not complete until the file is written to disk.

## Pitfalls

- pokemon_get_species MUST be called BEFORE pokemon_get_evolution — the evolution endpoint requires the evolution_chain_id from the species response. Calling evolution before species will fail or return no data.
- The existing skill originally described only 3 endpoints (details, moves, abilities). Tasks may require 4 endpoints (details, species, evolution, moves). Always check the task spec for the exact endpoint set and override the skill when they differ.
- The agent in this session collected details and species data but never saved the final output file. Always complete the full pipeline including writing the JSON file to disk — the task is scored on file presence, not just data collection.
- When parallelizing API calls across multiple Pokemon, ensure the species→evolution dependency is respected per Pokemon even if details and species calls are batched together.
