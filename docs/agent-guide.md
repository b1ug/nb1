# Use the blink1-control Skill

[简体中文](agent-guide.zh-CN.md)

The [Skill](../skills/blink1-control/SKILL.md) teaches an agent how to use an
existing nb1 CLI, HTTP API or MCP connection. It supplies no driver, daemon,
MCP registration or extra permissions. Use matching Skill and nb1 interfaces;
the installed help and MCP schemas describe what is available.

The [matching candidate guide](candidate-install.md#add-mcp-or-the-skill-independently)
covers verification and unpacking of the independent MCP and Skill artifacts.

## Add it to one project

Copy the entire `skills/blink1-control` directory from a matching nb1 checkout
into your project's `.agents/skills/`. Keep `references/` and `agents/`. Check for
an existing copy before replacing it.

Open a fresh Codex session in that project and check `/skills`. Invoke it with
`$blink1-control`, or describe a blink(1)/nb1 task naturally. Codex uses a Skill's
name and description for selection, then reads its body. The bundled metadata
allows implicit invocation. See the [official OpenAI Skills guide](https://learn.chatgpt.com/docs/build-skills).

Example requests:

```text
$blink1-control Preview a lime pulse twice. Do not play it.
Use nb1 to make my blink(1) breathe teal twice and finish off.
Save that as a local Pattern, without playing it.
```

CSS animations, website rainbow icons and unrelated USB devices are outside
this Skill's scope.

## Provide a connection

For daemon operation, start nb1 explicitly or use an already configured service:

```sh
nb1 server --devices DEVICE_SERIAL
```

The HTTP CLI selects its endpoint with `--endpoint` or profile `server.listen`.
The [MCP adapter](../adapters/mcp/README.md) takes its own `--endpoint` and does
not read nb1 YAML. Give the agent the intended endpoint and device, especially
when several installations exist. A missing daemon produces a connection error;
the Skill does not authorize automatic startup or USB fallback. Explicit direct
mode has a separate lifecycle.

An MCP host can separately require approval for saving or playback tools. A
blocked call is not a successful save or run. Approve only the intended server
and tools; loading this Skill is not a reason to relax the shell sandbox. See
[Codex MCP tool policy](https://learn.chatgpt.com/docs/extend/mcp?surface=cli).

Inspect available tools or `nb1 control --help`, preview, then execute the
requested task. HTTP202 is acceptance; terminal Job status is completion.
Keep the original endpoint, input and Job ID when a response is lost. A missing
record cannot prove that playback never ran.

For optional turn-ended lighting, use the [Codex notify guide](../examples/command-notify/CODEX.md).
It separates external notify JSON arguments from stdin Stop hooks, previews by
default, and shows a one-invocation configuration without global installation.
Its blue notification means a turn ended; inspect the Job report for playback.

## Read only the details needed

The main body covers expression choice, preview/run/track and recovery. Bundled
references remain usable after copying the Skill folder on its own:

| Need | Reference |
| --- | --- |
| CLI JSON versus MCP arguments, required fields, defaults, colors | [Expressions](../skills/blink1-control/references/expressions.md) |
| Named colors and computer-side Patterns | [Local library](../skills/blink1-control/references/patterns.md) |
| Explicit direct mode, configuration and device storage | [Direct and configuration](../skills/blink1-control/references/direct-and-config.md) |
| Complete limits, HTTP errors and examples | [API v1](api-v1.md) and [OpenAPI](api-v1.openapi.json) |

Host Patterns differ from device RAM, Flash and startup effects. Preview,
terminal status, timestamped RGB and camera observations are separate evidence.
See [platform validation](daemon-validation.md).

## Check an agent integration

Test relevant prompts and prompts that should not select the Skill. With a
software device, inspect requests and Job status rather than only final prose.
Useful cases include preview/save without playback, one finite run, lost Job
recovery and an unavailable daemon without automatic fallback.

Select Luna explicitly for a focused check:

```sh
codex exec --model gpt-6-luna
```

Use the available reasoning setting and your own task/connection. See
[official model documentation](https://learn.chatgpt.com/docs/models?surface=cli).
A finite test does not establish every model, hardware or failure scenario.
