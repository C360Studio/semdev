# Design: Add Dark Mode

## Technical Approach
A ThemeContext drives a data-theme attribute on the document root; colors resolve through CSS custom properties so a switch needs no re-render.

## Architecture Decisions

### Decision: CSS custom properties over CSS-in-JS
Custom properties let the theme switch without re-rendering the tree and keep the palette in one place.

### Decision: Persist in localStorage, not a cookie
The preference is client-only and never needs to reach the server.

## Data Flow
Settings toggle -> ThemeContext -> data-theme attribute -> CSS variable cascade.

## File Changes
- `src/theme/ThemeContext.tsx` (new)
- `src/styles/tokens.css` (modified)
- `src/legacy/themeShim.js` (deleted)
