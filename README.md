# Relay

A lightweight transaction relay service for BSV development that fund fees, and broadcasts transactions.

# Goal

reducing the boring stuffs like fee hanlding, broadcasting because this is repetivive and same for every transaction
that is why this project is made to get started withing 1 minute not more than that.

Note: this project is for local development only.

## Keys Info

- Keys lives under the .key directory .key/wif.txt
  automatically generated are listed as these
  - .key/wif.txt
  - .key/mnemonic.txt
  - .key/address.txt
  - .key/pubkey.txt

if you want to use exising key just add the wif.txt (only wif is requried) in project root .key directory as wif.txt

## How to start

This project was made for absolute minimal set up.
3(necessary)+1(optional) things just need to do is:

- adding key wif.txt in the respective directory (optional due to this server will generate wif.txt if not preset and this is recommended)
- add the arc.token in config
- mv config.example.yaml config.yaml
- docker compose up/podman compose up

## Config (optional to read)

- config will have its default port 8080, and default db test.db(sqlite), woc token is not necessary for most usecase
- fee.sat_per_byte is 100 since nov 15 (if i remember correctly). so change it once this rule changes, till then dont touch
- arc.token is necessary. by default it uses taal.arc so provide the token as per the app.network (test/main)

## for getting the fee rate

```bash
curl --location 'https://arc.taal.com/v1/policy' | jq
```
