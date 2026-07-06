# Design: Add Multi-Factor Authentication

## Technical Approach
Layer a TOTP challenge between password verification and session issuance; store per-user TOTP secrets encrypted at rest.

## Architecture Decisions

### Decision: TOTP over SMS
TOTP avoids SIM-swap attacks and needs no SMS gateway dependency.

## Data Flow
Password verified -> TOTP prompt -> code validated -> session issued.

## File Changes
- `auth/mfa/totp.go` (new)
- `auth/session.go` (modified)
