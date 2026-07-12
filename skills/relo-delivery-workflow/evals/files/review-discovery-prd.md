# Account service PRD

Implement account persistence and expose account creation through an HTTP API. The persistence task must complete before the API task. Add a checkpoint after the API is available to review end-to-end account creation, transaction handling, and authorization behavior.

During the checkpoint fixture, persistence and API tasks are already passed, but the review reveals that authorization was not implemented and is distinct work rather than a missed persistence acceptance criterion.
