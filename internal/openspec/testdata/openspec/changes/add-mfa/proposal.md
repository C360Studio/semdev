# Proposal: Add Multi-Factor Authentication

## Intent
Account compromise from password-only login is the top security risk; a second factor mitigates it.

## Scope
In scope:
- TOTP second factor for accounts that enable it
- Tightened idle-session expiry

Out of scope:
- Hardware security keys (WebAuthn)

## Approach
Add a TOTP challenge after password verification and shorten the idle-session window.
