---
name: Weather Monitor - Multi-City
description: Collects coordinates, current weather, hourly forecast, and daily forecast for N cities via four weather API endpoints per city, then compiles results into a structured JSON report with global summary statistics comparing all cities.
---

# Weather Monitor - Multi-City

## When to use

When the task requires creating a weather report or collecting multi-dimensional weather data (coordinates, current conditions, hourly forecast, daily forecast) for one or more cities and saving the compiled result as a JSON file.

## Steps

1. If a workspace contains a collections.json or similar helper file, read it first to understand any additional collection parameters that may refine the output.
2. For each target city, call `local-weather_get_coordinates` FIRST with city_name and country to obtain latitude, longitude, timezone, and metadata. This is mandatory before other weather tools.
3. For each city, after coordinates are obtained, call `local-weather_get_current` with latitude and longitude to get current weather conditions.
4. For each city, call `local-weather_get_hourly` with latitude and longitude to get the 168-hour forecast.
5. For each city, call `local-weather_get_daily` with latitude and longitude to get the 14-day daily forecast (includes daily max/min temps, sunrise/sunset, UV index, precipitation sums, wind data).
6. Compile all city data into a structured JSON object with a `cities` array (each city having name, coordinates, current, hourly_forecast, daily_forecast) and a `global_summary` object with cross-city statistics such as total_cities, warmest_city, and coldest_city.
7. Save the compiled result to the required output file (e.g. `weather_report.json`) using `mcp_local-write_final_json` with the correct path. Verify the write completes successfully and the file exists before considering the task done.

## Pitfalls

- The final JSON write MUST complete successfully. In a prior run, the agent called the write tool but the output file was not found — the task scored zero. Always confirm the write finished and the file is present at the expected path.
- Coordinates must be obtained first for every city before calling current/hourly/daily endpoints — those tools require latitude and longitude from the coordinates call.
- Each city object in the output should include all four data dimensions (coordinates, current, hourly_forecast, daily_forecast). Omitting the daily_forecast (which was not in the original 3-endpoint version of this skill) will produce incomplete output.
