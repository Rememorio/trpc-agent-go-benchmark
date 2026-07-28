---
name: Create Economic Snapshots for Multiple Countries
description: Create economic snapshots for specified countries by collecting data through multiple API endpoints including economic overviews, GDP data, and country information, then compiling the results into a structured JSON report.
---

# Create Economic Snapshots for Multiple Countries

## When to use

When you need to analyze and summarize economic data for three or more countries using various APIs.

## Steps

1. Define the list of countries and their codes.
2. For each country, call all required API endpoints (economic snapshot, GDP, country info, and indicators if applicable) to collect data in one pass.
3. Calculate economic power rank and development tier for each country based on GDP per capita.
4. Compile all data into a single JSON report and save it to the specified location.
5. Call claim_done to complete.

## Pitfalls

- Ensure the country codes are correct to avoid data retrieval errors.
- Verify that all required indicators are included in the final report.
