# booksy-cli

Terminal client for Booksy: find businesses, check open slots, watch for cancellations, and book, against Booksy's unofficial customer web API.

## Language

### Marketplace

**Business**:
A place you book at (a barbershop, a salon), identified by a numeric id. Its booksy.com URL path starts with that id (`3349_barberia-..._madrid`).
_Avoid_: Salon, shop, venue, provider

**Service**:
Something a Business offers, such as "Corte de pelo". Grouped under the Business's own service categories.

**Variant**:
The bookable form of a Service: one duration and one price. Every booking and every slot query is for a Variant, never for a Service.
_Avoid_: Option, package

**Treatment**:
An entry of Booksy's shared catalogue ("Recorte de la barba", 71) that every Service is filed under. Service names belong to each Business; the Treatment is how one kind of service is compared across Businesses.
_Avoid_: Category (that is the Business taxonomy), service type

**Staffer**:
A person at the Business who performs Services. "Any staffer" is id -1 on the wire.
_Avoid_: Employee, resource (Booksy's internal word, which also covers rooms and equipment)

**Category**:
Booksy's global taxonomy of Businesses (Barbería is 45). Distinct from a Business's service categories.

**Slot**:
One open start time for one Variant (and optionally one Staffer), in the Business's local time, written `2026-10-28T13:00`.
_Avoid_: Time slot, opening, availability (that is the list of Slots)

**Schedule**:
When the user can go, as `--when` entries: days plus an optional time range, with date entries overriding weekday ones. Narrows Slots in `slots`, `book --first`, watches and `earliest`.
_Avoid_: Availability (that is the business's open Slots)

### Booking

**Draft**:
A temporary booking cart on Booksy's side, created per attempt. Changing its time, staffer or note bumps its version. A Draft books nothing until it is scheduled.
_Avoid_: Cart, pending appointment

**Appointment**:
A booking on the account, identified by its uid. It has a status (confirmed, cancelled, finished, no_show) and a version that cancelling must echo.
_Avoid_: Booking (Booksy's list endpoint calls them that, but the CLI says Appointment), reservation, visit

**Reschedule**:
Moving an Appointment to another Slot through an edit Draft, keeping its Service and Staffer.

### Tracking

**Watch**:
A saved instruction to look for newly opened Slots for one Variant at one Business, narrowed by a Slot filter. Lives locally, never on Booksy.
_Avoid_: Alert, tracker, subscription

**Check**:
One run of a Watch: fetch the open Slots, compare with the Slots seen last time, emit Events, store the new state.

**Event**:
One `slots.opened` record from a Check: the Slots that opened since the previous Check, earliest first.
_Avoid_: Notification (that is the delivery)

**Horizon**:
The last date the previous Check could see. Slots past it are the booking window rolling forward, not cancellations, and do not produce Events.

**Sink**:
Where an Event is delivered besides stdout: ntfy, a webhook, or an executable.

### Account

**Profile**:
One named login known to the CLI, tied to one country. One Profile is the default.
_Avoid_: Account, user

**Access token**:
The value of booksy.com's `identity-<country>` cookie, sent as `X-Access-Token`. Long-lived (about six months) and never rotated.
_Avoid_: Session, cookie, JWT

**Fingerprint**:
A stable per-Profile device id sent as `X-Fingerprint`. Booksy refuses to create Drafts without one. Imported from the `_bf` cookie when present, otherwise generated.

**Bookmark**:
A Business saved on the account.
