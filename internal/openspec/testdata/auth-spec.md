# Auth Specification

## Purpose
User authentication and session management for the application.

## Requirements

### Requirement: Password Login
The system SHALL allow a registered user to authenticate with email and password.

#### Scenario: Valid credentials
- GIVEN a registered user with a valid password
- WHEN the user submits the correct email and password
- THEN the system grants an authenticated session
- AND the session persists until logout or expiry

#### Scenario: Invalid password
- GIVEN a registered user
- WHEN the user submits an incorrect password
- THEN the system rejects the login
- AND the system does not reveal whether the email exists

### Requirement: Session Expiry
The system SHALL expire an idle session after 30 minutes of inactivity.

#### Scenario: Idle timeout
- GIVEN an authenticated session
- WHEN 30 minutes elapse with no activity
- THEN the system invalidates the session
