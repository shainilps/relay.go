# Some Random Research

## Deteriming the exact size of the input, and ouput

transaction format:

[4]
[var-(1:9)]
{[32(txid)-4(vout)-var-(1:9)(unlocking-script-size)-(unlocking-script)-sequence(4)]}
[var-(1:9)]
{[8(satoshi)-var(1:9)-(locking-script)]}
[4]

[version][number-of-input][(txid-vout-unlockingscript-sequence)][number-of-outputs][(satoshi)-(lockingscriptsize)-(lockingscript)][locktime]

```
[version:4]

[input_count:varint]
  [
    txid:32
    vout:4
    scriptSig_size:varint
    scriptSig:scriptSig_size
    sequence:4
  ] * input_count

[output_count:varint]
  [
    value:8
    scriptPubKey_size:varint
    scriptPubKey:scriptPubKey_size
  ] * output_count

[locktime:4]

```

## P2PKH:

**unlockingScript**: == (106-108)

[(v-size)signature + sighashtype] [(v-size)pubkey]
[(1)(72 + 1)][(1)(33)]
74+34 = 108

signature = 6 + len(r)+len(s)[32+1 - 32+1] == 72
pubke (compressed) = 33

**lockinggscript** == 26
[(v-size)(lockingscript)]
[(1)(25)]

why 25 ?
[op_dup op_hash160 op_data20 pubkeyhash op_equalverify op_checksig]

# conclusion

- we can consider the varint only `1` becuase we wont add more than 256 input or 256 output eithers (in our case)

now i think we can guess the input and output size correctly(through proper math(almost accurate))

one input cost = 32+4+1+(106-108)+4 = 147-149 (we will consider lowerbound)
one output cost = 8+1+25 = 34 bytes
