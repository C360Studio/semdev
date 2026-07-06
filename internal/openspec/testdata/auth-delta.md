# Delta for Auth

## ADDED Requirements

### Requirement: Multi-Factor Authentication
The system SHALL require a second factor for accounts with MFA enabled.

#### Scenario: TOTP challenge
- GIVEN a user with MFA enabled
- WHEN the user submits a valid password
- THEN the system prompts for a TOTP code
- AND grants the session only after a valid code

## MODIFIED Requirements

### Requirement: Session Expiry
The system SHALL expire an idle session after 15 minutes of inactivity.
(Previously: The system SHALL expire an idle session after 30 minutes of inactivity.)

#### Scenario: Idle timeout
- GIVEN an authenticated session
- WHEN 15 minutes elapse with no activity
- THEN the system invalidates the session

## REMOVED Requirements

### Requirement: Remember Me
(Rationale: Long-lived sessions conflict with the new MFA policy.)
