# relay

A lightweight transaction relay service for BSV development that fund fees, and broadcasts transactions.

# Project Thoughts

- during this project time the BRC100 also kinda is becoming std in bsv ecosystem, BRC100 is one word is you make a token and you make server for indexing the token all the tx go through you (term is overlay, little more than that, but we have to see the adoption for this anyway)
- first thing i didn't do is not adding interface for arc and broadcaster clinet because i think i wont switch broadcaster for now (we can do this later if it really required)
  and anyway arc and broadcast is gonna replaced with overlay lookup and topic manager
- this service can be kept between server->relay->overlay
