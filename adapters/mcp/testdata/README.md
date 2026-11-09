Fixtures copied from the pure Go HTTP/core light preview proof used for commit 9e891f6. They are test data, not a second color/effect compiler. The MCP module imports no parent module. Tests add only target snapshot metadata and deliberate response corruptions.

`light-flow-v1.json` freezes eight results from `lightaction.CompileJSON` and
`control.Preview` at integrated base `f395eb17ac5b830fd250ea7f01ab3beb753cc001`.
They cover omitted transition, explicit cut, the three continuous modes,
reversed LED order, uneven slots, the minimum/maximum period and 15 cycles.
They were generated in a temporary pure-Go copy with Go1.18.10, without HTTP or
devices. The test module embeds these results and does not import that compiler.
