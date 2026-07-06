# Proposal: Add Dark Mode

## Intent
Users have requested a dark mode option to reduce eye strain during nighttime usage.

## Scope
In scope:
- Add a theme toggle in settings
- Support system preference detection

Out of scope:
- Per-component theme overrides

## Approach
Introduce a ThemeContext backed by a CSS custom-property palette and persist the choice in localStorage.
