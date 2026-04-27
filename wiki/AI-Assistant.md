# AI Assistant

calm-axolotl ships with an optional Claude-powered chat that can read
and modify your calendars on your behalf. Every write requires you to
click **Apply** before it actually hits Google.

## Enabling it

Add to `.env`:

```ini
ANTHROPIC_API_KEY=sk-ant-...
ANTHROPIC_MODEL=claude-opus-4-7
```

Restart the daemon. An **Assistant** link appears in the nav.

If `ANTHROPIC_API_KEY` is unset, the routes are unregistered, the nav
link is hidden, and no part of the assistant code path executes.

## How it works

The assistant is a tool-use loop on top of Anthropic's
[Messages API](https://docs.anthropic.com/en/api/messages). When you
send a message, calm-axolotl:

1. Persists your message into Postgres.
2. Sends the conversation + the available tools to Claude.
3. If Claude responds with **tool calls**:
   - **Read tools** (list, search, find_free_time) execute immediately,
     and the results go back to Claude for the next turn.
   - **Write tools** (create, update, delete, move) are *staged* — they
     show up in the chat as confirmation cards. Nothing hits Google
     until you click **Apply**.
4. When you apply or reject every staged action, calm-axolotl sends the
   results back to Claude and the loop continues until the assistant
   ends its turn with a text reply.

## Available tools

### Read-only (auto-execute)

| Tool                | What it does                                                  |
| ------------------- | ------------------------------------------------------------- |
| `list_calendars`    | Lists every connected calendar (id, summary, account, tz)    |
| `list_events`       | Lists events on a calendar within a time range               |
| `find_event`        | Searches event summaries across all calendars                |
| `find_free_time`    | Returns free windows for given calendars + duration          |

### Write (require confirmation)

| Tool             | What it does                                                |
| ---------------- | ----------------------------------------------------------- |
| `create_event`   | Creates a new event on a calendar                           |
| `update_event`   | Modifies summary/time/location/description on an event      |
| `delete_event`   | Removes an event                                            |
| `move_event`     | Convenience for changing only start+end                     |

All writes carry `extendedProperties.private.calmAxolotlManaged="1"`
plus `calmAxolotlAiSession=<conversation_id>` so they're attributable
later and don't trigger sync rules as a feedback loop.

## Conversation persistence

- Conversations are stored in Postgres (`ai_conversation`,
  `ai_message`, `ai_pending_action`).
- They are auto-deleted **30 days** after the last message.
- You can also delete a conversation manually at any time from the
  conversations list.

## Audit log

Every write the assistant performs lands in the audit log with
`kind="ai"` and the conversation ID in the message field.

## Examples

> **You:** "Move my dentist appointment from Thursday to Friday at the
> same time, please."
>
> *(assistant calls `find_event(query="dentist")` → result lists the
> Thursday event)*
>
> *(assistant calls `move_event(...)` → confirmation card shown)*
>
> Click **Apply**. Move happens. Assistant: "Done — moved to Friday at
> 3pm. Anything else?"

> **You:** "Find me 90 minutes for deep work tomorrow morning before
> noon."
>
> *(assistant calls `find_free_time(...)` → returns three slots)*
>
> Assistant: "You have 9:00–10:30 and 10:45–12:00 free. Want me to put
> a Focus block on the longer one?"

> **You:** "Yes, on my work calendar."
>
> *(assistant calls `create_event(...)` → confirmation card shown)*
>
> Click **Apply**. Done.

## Limits & costs

- The assistant uses your `ANTHROPIC_API_KEY` directly. You pay
  Anthropic for tokens consumed; calm-axolotl doesn't proxy through
  any third party.
- Conversations capped at the model's context window — the daemon
  doesn't auto-truncate, so very long chats may eventually fail. If
  that happens, start a new conversation.
- The assistant has no awareness of your sync rules or smart blocks —
  it operates at the calendar/event level. Manipulating a managed
  event directly might fight your rules.

## Why confirmation is required

LLMs misinterpret things. "Cancel my Tuesday meeting" might mean the
1pm with Acme but the model decides it's the recurring 3pm with the
team. Or "move my doctor appointment" hits a different doctor than the
one you meant. Confirmation cards give you the chance to catch that
before Google gets the call.

If you want full autonomy on a per-conversation basis, that's a feature
we may add later — open an issue if you need it.
