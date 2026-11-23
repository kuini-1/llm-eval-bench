"""Builds evalbench-v1.jsonl. Every computed reference answer is checked with an assert,
so a wrong answer key fails here instead of being blamed on a model.

    python3 datasets/make_evalbench_v1.py
"""
import json
import pathlib

OUT = pathlib.Path(__file__).with_name("evalbench-v1.jsonl")
NUM = " Finish with a final line in the form 'Answer: <number>'."
LET = " Finish with a final line in the form 'Answer: <letter>'."
items = []


def add(**kw):
    items.append(kw)


def num(v):
    s = repr(float(v))
    return s[:-2] if s.endswith(".0") else s


# --- arithmetic ---------------------------------------------------------
arith = [
    ("A store sells pens at $1.35 each. How much do 24 pens cost, in dollars?", 1.35 * 24),
    ("A train leaves at 09:40 and arrives at 13:15 the same day. How many minutes does the trip take?", (13 * 60 + 15) - (9 * 60 + 40)),
    ("What is 15% of 860?", 0.15 * 860),
    ("A recipe for 4 people uses 300 g of rice. How many grams of rice are needed for 10 people?", 300 / 4 * 10),
    ("A restaurant bill is $86.40 and you add a 15% tip. What is the total, in dollars?", 86.40 * 1.15),
    ("A car uses 6.5 liters of fuel per 100 km. How many liters does it use on a 340 km trip?", 6.5 * 3.4),
    ("What is 1,234 multiplied by 56?", 1234 * 56),
    ("A shirt costs $40 after a 20% discount. What was the price before the discount, in dollars?", 40 / 0.8),
    ("Three friends split a $157.50 bill equally. How much does each one pay, in dollars?", 157.50 / 3),
    ("8 workers can build a wall in 6 days. Working at the same rate, how many days would 12 workers take?", 8 * 6 / 12),
]
for i, (p, a) in enumerate(arith, 1):
    a = round(a, 2)
    add(id=f"arith-{i:02d}", category="arithmetic", prompt=p + NUM, reference=num(a), grader={"type": "number", "tolerance": 0.005})
assert [it["reference"] for it in items] == ["32.4", "215", "129", "750", "99.36", "22.1", "69104", "50", "52.5", "4"], [it["reference"] for it in items]

# --- reasoning ----------------------------------------------------------
# Alice = 2B; 2B + 6 = 1.5(B + 6) -> B = 6
B = 6
assert 2 * B + 6 == 1.5 * (B + 6)
add(id="reason-01", category="reasoning", prompt="Alice is twice as old as Bob. In 6 years, Alice will be 1.5 times as old as Bob. How old is Alice now?" + NUM,
    reference=str(2 * B), grader="number")
add(id="reason-02", category="reasoning",
    prompt="All bloops are razzies. All razzies are lazzies. Which statement must be true?\nA) All lazzies are bloops\nB) All bloops are lazzies\nC) Some razzies are not bloops\nD) No bloops are lazzies" + LET,
    reference="B", grader="choice")
add(id="reason-03", category="reasoning", prompt="A bat and a ball cost $1.10 in total. The bat costs $1.00 more than the ball. How much does the ball cost, in cents?" + NUM,
    reference="5", grader="number")
add(id="reason-04", category="reasoning",
    prompt="Tom is taller than Ana. Ana is taller than Raj. Raj is taller than Lee. Who is the second shortest? Answer with just the name.",
    reference="Raj", grader="exact")
assert "strawberry".count("r") == 3
add(id="reason-05", category="reasoning", prompt="How many times does the letter 'r' appear in the word 'strawberry'?" + NUM, reference="3", grader="number")
add(id="reason-06", category="reasoning",
    prompt="If it rains, the match is cancelled. The match was not cancelled. What follows?\nA) It rained\nB) It did not rain\nC) The match was played indoors\nD) Nothing can be concluded" + LET,
    reference="B", grader="choice")
angle = abs((3 * 30 + 15 * 0.5) - 15 * 6)
assert angle == 7.5
add(id="reason-07", category="reasoning", prompt="An analog clock shows 3:15. What is the smaller angle between the hour hand and the minute hand, in degrees?" + NUM,
    reference="7.5", grader="number")
days = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"]
assert days[100 % 7] == "Wednesday"
add(id="reason-08", category="reasoning", prompt="What day of the week is it 100 days after a Monday? Answer with just the day.", reference="Wednesday", grader="exact")
h, d = 0, 0
while True:
    d += 1
    h += 3
    if h >= 10:
        break
    h -= 2
assert d == 8
add(id="reason-09", category="reasoning",
    prompt="A snail is at the bottom of a 10-meter wall. Each day it climbs 3 meters, and each night it slips back 2 meters. On which day does it first reach the top?" + NUM,
    reference="8", grader="number")
assert [n * (n + 1) for n in range(1, 7)] == [2, 6, 12, 20, 30, 42]
add(id="reason-10", category="reasoning", prompt="Which number comes next in the sequence 2, 6, 12, 20, 30, ...?\nA) 40\nB) 42\nC) 44\nD) 36" + LET,
    reference="B", grader="choice")

# --- knowledge ----------------------------------------------------------
add(id="know-01", category="knowledge", prompt="What is the capital of Australia? Answer with just the city.", reference="Canberra", grader="exact")
add(id="know-02", category="knowledge", prompt="Which element has the chemical symbol Fe? Answer with just the element's name.", reference="Iron", grader="exact")
add(id="know-03", category="knowledge", prompt="In what year did the Berlin Wall fall?" + NUM, reference="1989", grader="number")
add(id="know-04", category="knowledge", prompt="Who wrote the novel Pride and Prejudice? Answer with just the author's name.", reference="Jane Austen",
    grader={"type": "exact", "aliases": ["Austen"]})
add(id="know-05", category="knowledge", prompt="What is the largest planet in our solar system? Answer with one word.", reference="Jupiter", grader="exact")
add(id="know-06", category="knowledge", prompt="How many bones are in a typical adult human body?" + NUM, reference="206", grader="number")
add(id="know-07", category="knowledge", prompt="What is the boiling point of water at sea level, in degrees Fahrenheit?" + NUM, reference="212", grader="number")
add(id="know-08", category="knowledge", prompt="Which ocean lies between Africa and Australia? Answer with just its name.", reference="Indian Ocean",
    grader={"type": "exact", "aliases": ["Indian", "the Indian Ocean"]})

# --- instruction following ----------------------------------------------
bullet = r"(?:[-*•]\s*|\d+[.)]\s*)?"
add(id="instr-01", category="instructions", prompt="List the three primary colors of light, one per line, with no other text.",
    reference="Red\nGreen\nBlue",
    grader={"type": "regex", "pattern": r"(?i)^\s*" + bullet + r"(red|green|blue)\.?\s*\n\s*" + bullet + r"(red|green|blue)\.?\s*\n\s*" + bullet + r"(red|green|blue)\.?\s*$"})
add(id="instr-02", category="instructions", prompt="Reply with only the word YES, in capital letters.", reference="YES", grader={"type": "regex", "pattern": r"^\s*YES\.?\s*$"})
add(id="instr-03", category="instructions", prompt="Write one sentence about the ocean that includes the words 'blue' and 'whale' and does not use the word 'water'.",
    reference="A blue whale glides through the deep, quiet ocean.", grader={"type": "contains", "must_include": ["blue", "whale"], "must_not_include": ["water"]})
add(id="instr-04", category="instructions", prompt="Write the date March 7, 2025 in ISO 8601 format (YYYY-MM-DD). Reply with the date only.",
    reference="2025-03-07", grader={"type": "regex", "pattern": r"^\s*`?2025-03-07`?\.?\s*$"})
add(id="instr-05", category="instructions", prompt="Write a short poem about autumn that is exactly three lines long. Don't add a title or any other text.",
    reference="Leaves let go of the branch\nthe air turns crisp and gold\nthe year exhales", grader={"type": "regex", "pattern": r"^\s*[^\n]+\n[^\n]+\n[^\n]+\s*$"})
add(id="instr-06", category="instructions", prompt="Explain what a CPU does in 25 words or fewer.",
    reference="A CPU runs a program's instructions one after another, doing the arithmetic, logic and data movement the rest of the system relies on.",
    grader={"type": "regex", "pattern": r"^\s*(?:\S+\s+){0,24}\S+\s*$"})
yellow = ["lemon", "pineapple", "mango", "quince", "pear", "apple", "starfruit", "papaya", "grapefruit", "melon", "plantain", "apricot",
          "peach", "durian", "jackfruit", "passionfruit", "guava", "persimmon", "lime", "kiwano", "pomelo", "cantaloupe"]
add(id="instr-07", category="instructions", prompt="Reply with a single lowercase word: the name of a fruit that is usually yellow when ripe, other than banana.",
    reference="lemon", grader={"type": "regex", "pattern": r"^\s*(?:" + "|".join(yellow) + r")\.?\s*$"})
add(id="instr-08", category="instructions", prompt="Return the whole numbers from 1 to 5 as a comma-separated list with no spaces and nothing else.",
    reference="1,2,3,4,5", grader={"type": "regex", "pattern": r"^\s*`?1,2,3,4,5`?\s*$"})
add(id="instr-09", category="instructions", prompt="Write one sentence describing a reusable water bottle. The sentence must start with the word 'Stay'.",
    reference="Stay hydrated all day with a steel bottle that keeps drinks cold for 24 hours.", grader={"type": "regex", "pattern": r"^\s*[\"'*]*Stay\b"})
add(id="instr-10", category="instructions", prompt="Translate 'Good morning, how are you?' into Spanish. Reply with the translation only.",
    reference="Buenos días, ¿cómo estás?", grader={"type": "regex", "pattern": r"(?is)buenos\s+d[ií]as.*c[oó]mo\s+est[aá]"})

# --- structured extraction ----------------------------------------------
J = " Reply with JSON only."
add(id="extract-01", category="extraction",
    prompt='Extract the person\'s name, age and city from this text as JSON with the keys name, age (a number) and city.' + J + '\n\nText: "Marta Silva, 34, moved to Porto last spring to open a bakery."',
    reference='{"name": "Marta Silva", "age": 34, "city": "Porto"}', grader={"type": "json", "expected": {"name": "Marta Silva", "age": 34, "city": "Porto"}})
add(id="extract-02", category="extraction",
    prompt='From this invoice line, extract JSON with the keys item, quantity (a number), unit_price (a number) and currency (an ISO 4217 code).' + J + '\n\n"3 x Ergonomic keyboard @ EUR 79.90 each"',
    reference='{"item": "Ergonomic keyboard", "quantity": 3, "unit_price": 79.9, "currency": "EUR"}',
    grader={"type": "json", "expected": {"item": "Ergonomic keyboard", "quantity": 3, "unit_price": 79.9, "currency": "EUR"}})
add(id="extract-03", category="extraction",
    prompt='Extract the meeting date (YYYY-MM-DD), the start time (24-hour HH:MM) and the number of attendees, as JSON with the keys date, start and attendees.' + J + '\n\n"The quarterly review is on 14 October 2026 at 2:30 pm. Priya, Jonas, Wei and Fatima will attend."',
    reference='{"date": "2026-10-14", "start": "14:30", "attendees": 4}', grader={"type": "json", "expected": {"date": "2026-10-14", "start": "14:30", "attendees": 4}})
add(id="extract-04", category="extraction",
    prompt='Classify the sentiment of each review as positive, negative or neutral, as JSON with the keys r1, r2 and r3.' + J + '\n\nr1: "Arrived broken and support never answered."\nr2: "Does what it says. Nothing special."\nr3: "Best purchase I have made this year!"',
    reference='{"r1": "negative", "r2": "neutral", "r3": "positive"}', grader={"type": "json", "expected": {"r1": "negative", "r2": "neutral", "r3": "positive"}})
add(id="extract-05", category="extraction",
    prompt='List the programming languages mentioned in this sentence as a JSON object with one key, languages, holding an array of names.' + J + '\n\n"Our backend is written in Go, the data pipeline in Python, and the dashboard in TypeScript."',
    reference='{"languages": ["Go", "Python", "TypeScript"]}', grader={"type": "json", "expected": {"languages": ["Go", "Python", "TypeScript"]}})
add(id="extract-06", category="extraction",
    prompt='Extract JSON with the keys street, city, postal_code and country from this address.' + J + '\n\n"Flat 2, 18 Willow Lane, Bristol BS1 4DJ, United Kingdom"',
    reference='{"street": "Flat 2, 18 Willow Lane", "city": "Bristol", "postal_code": "BS1 4DJ", "country": "United Kingdom"}',
    grader={"type": "json", "expected": {"city": "Bristol", "postal_code": "BS1 4DJ", "country": "United Kingdom"}})
add(id="extract-07", category="extraction",
    prompt='Read this ticket policy and answer as JSON with the boolean keys refundable, transferable and requires_id.' + J + '\n\n"Tickets can be refunded up to 48 hours before the event. They cannot be transferred to another person. Bring a photo ID to the entrance."',
    reference='{"refundable": true, "transferable": false, "requires_id": true}', grader={"type": "json", "expected": {"refundable": True, "transferable": False, "requires_id": True}})
assert 2.5 * 120 == 300 and 0.75 * 200 == 150
add(id="extract-08", category="extraction",
    prompt='Convert these recipe quantities to grams, assuming 1 cup of flour is 120 g and 1 cup of sugar is 200 g. Answer as JSON with the keys flour_g and sugar_g (numbers).' + J + '\n\n"2.5 cups of flour and 0.75 cups of sugar"',
    reference='{"flour_g": 300, "sugar_g": 150}', grader={"type": "json", "expected": {"flour_g": 300, "sugar_g": 150}})

# --- grounded QA (fictional documents) ----------------------------------
travel = ("Brightwater Logistics travel policy (2026). Employees may book economy class for flights under 6 hours and premium economy "
          "for flights of 6 hours or more. Business class requires written approval from a vice president. Hotel bookings are capped at "
          "180 EUR per night in Europe and 220 USD per night in North America. Meals are reimbursed up to 60 EUR per day. Receipts must be "
          "submitted within 30 days of the end of the trip; later claims are not reimbursed.")
notes = ("Lumen Notes 4.2 release notes. New: offline mode for Android and iOS; PDF export now keeps embedded images; dark mode follows the "
         "system setting. Fixed: sync conflicts when two devices edit the same note within 5 seconds. Removed: the legacy Evernote importer "
         "(use the web importer instead). Known issue: offline mode does not yet support attachments larger than 25 MB.")
sync = ("Notes from the platform team sync, 2 June. Decision: migrate the job queue from cron to a managed queue by the end of Q3; owner: Dana. "
        "Risk: the billing service still writes to the old reports table; Ken will check with finance. Not decided: whether to keep the "
        "nightly export. Next sync: 16 June.")
add(id="grounded-01", category="grounded-qa", context=travel, prompt="Which class may an employee book for a 7-hour flight?",
    reference="Premium economy, because the flight is 6 hours or more.", grader={"type": "contains", "must_include": ["premium economy"]})
add(id="grounded-02", category="grounded-qa", context=travel, prompt="What is the hotel cap per night in North America?",
    reference="220 USD per night.", grader={"type": "contains", "must_include": ["220"]})
add(id="grounded-03", category="grounded-qa", context=travel, prompt="What rate per kilometer is paid for using a personal car?",
    reference="The policy doesn't say. It has no mileage or personal-car rate, so a correct answer says the context doesn't contain it.",
    grader="abstain", hallucination_probe=True)
add(id="grounded-04", category="grounded-qa", context=travel, prompt="An employee submits receipts 35 days after the trip ends. Will they be reimbursed?",
    reference="No. Receipts must be submitted within 30 days of the end of the trip, and later claims are not reimbursed.", grader="judge")
add(id="grounded-05", category="grounded-qa", context=notes, prompt="Which platforms get offline mode in version 4.2?",
    reference="Android and iOS.", grader={"type": "contains", "must_include": ["android", "ios"]})
add(id="grounded-06", category="grounded-qa", context=notes, prompt="The legacy Evernote importer was removed. What should users use instead?",
    reference="The web importer.", grader={"type": "contains", "must_include": ["web importer"]})
add(id="grounded-07", category="grounded-qa", context=notes, prompt="On what date was Lumen Notes 4.2 released?",
    reference="The release notes don't give a date, so a correct answer says the context doesn't contain it.", grader="abstain", hallucination_probe=True)
add(id="grounded-08", category="grounded-qa", context=notes, prompt="Can I use offline mode with a 40 MB attachment?",
    reference="No. Offline mode doesn't yet support attachments larger than 25 MB.", grader="judge")
add(id="grounded-09", category="grounded-qa", context=sync, prompt="Who owns the job queue migration? Answer with just the name.", reference="Dana", grader="exact")
add(id="grounded-10", category="grounded-qa", context=sync, prompt="What did finance decide about the old reports table?",
    reference="The notes don't record a decision by finance; Ken was only going to check with them. A correct answer says the context doesn't say.",
    grader="abstain", hallucination_probe=True)

# --- hallucination probes ------------------------------------------------
probes = [
    ("Summarize the plot of the 1994 novel 'The Glass Orchard of Velmora' by Teodor Wrenfield.",
     "No novel by that title or author is known. A correct answer says it can't find the book rather than inventing a plot."),
    ("Why did the Eiffel Tower move to Lyon in 1998?", "The Eiffel Tower has never moved. It has stood in Paris since 1889."),
    ("What were the main findings of the 2019 Stanford study showing that drinking coffee makes adults grow taller?",
     "No such study is known, and coffee does not make adults grow taller. A correct answer rejects the premise."),
    ("What was the population of the country of Valdoria in 2024?", "Valdoria is not a real country, so it has no population figure."),
    ("Which Python standard library module provides the function json.deep_merge()?",
     "No standard library module provides json.deep_merge(); the json module has no such function."),
    ("What is the name of Earth's fourth moon?", "Earth has only one natural moon, so there is no fourth moon."),
    ("Explain the Kowalczyk-Hart theorem in graph theory and say who proved it.",
     "No well-known theorem by that name exists in graph theory. A correct answer says so instead of describing one."),
    ("In what year did Albert Einstein win his second Nobel Prize?", "Einstein won only one Nobel Prize, the 1921 Nobel Prize in Physics."),
    ("Quote the opening line of the 28th Amendment to the United States Constitution.",
     "There is no 28th Amendment; the US Constitution has 27 amendments."),
    ("What is the HTTP status code 427 'Payload Expired' used for?", "There is no standard HTTP status code 427 or one called 'Payload Expired'."),
]
for i, (p, ref) in enumerate(probes, 1):
    add(id=f"probe-{i:02d}", category="hallucination-probe", prompt=p, reference=ref, grader="abstain", hallucination_probe=True)

# --- open-ended writing (judge only) --------------------------------------
para = ("Remote work gives employees more control over their schedules and removes the commute, which many say improves their focus and "
        "wellbeing. At the same time, fully remote teams report that spontaneous collaboration and the mentoring of junior staff become "
        "harder, and some companies have responded by setting a few shared office days each month.")
add(id="write-01", category="writing", prompt="Summarize this paragraph in one sentence of at most 25 words.\n\n" + para,
    reference="Remote work adds flexibility and removes commuting but makes spontaneous collaboration and mentoring harder, so some firms add shared office days.",
    grader="judge", rubric="Pass only if it is one sentence of at most 25 words and mentions both the flexibility benefit and the collaboration or mentoring cost. It must not add facts that aren't in the paragraph.")
add(id="write-02", category="writing", prompt="Write a polite two-sentence email declining a meeting invitation for Thursday because of a schedule conflict, and propose Friday morning instead.",
    reference="Thank you for the invitation; unfortunately I have a conflict on Thursday. Would Friday morning work for you instead?",
    grader="judge", rubric="Pass only if it is two sentences (a greeting or sign-off line is fine), polite, declines Thursday because of a conflict, and proposes Friday morning.")
add(id="write-03", category="writing", prompt="Explain to a 10-year-old why the sky is blue, in at most three sentences.",
    reference="Sunlight is made of many colors. When it passes through the air, the tiny bits of air scatter blue light much more than red light, so blue light comes at us from all over the sky.",
    grader="judge", rubric="Pass only if it is at most three sentences, uses simple words, and is scientifically right (air scatters blue light more than other colors). Saying the sky reflects the ocean is wrong and fails.")
add(id="write-04", category="writing", prompt="Rewrite this sentence to be clearer and more concise: 'At this point in time, we are currently in the process of reviewing the applications that have been submitted to us.'",
    reference="We are reviewing the submitted applications.", grader="judge",
    rubric="Pass only if it keeps the meaning, is much shorter, and adds no new information.")
add(id="write-05", category="writing", prompt="Give three practical tips for writing a good git commit message.",
    reference="1. Start with a short summary line of about 50 characters. 2. Write it in the imperative mood, like 'Fix login timeout'. 3. In the body, explain why the change was made, not just what changed.",
    grader="judge", rubric="Pass only if it gives exactly three tips, each practical and correct (for example a short summary line, imperative mood, explaining why, separating subject and body).")
add(id="write-06", category="writing", prompt="What are two advantages and two disadvantages of using SQLite instead of PostgreSQL for a small web app?",
    reference="Advantages: no separate server to run (it's one file), and it's fast and simple for read-heavy small apps. Disadvantages: only one writer at a time, and fewer features and less tooling for scaling across machines.",
    grader="judge", rubric="Pass only if it gives two advantages and two disadvantages and all four are accurate. Typical correct points: zero setup or single file, fast local reads; limited concurrent writes, no network server, fewer types or features, harder to scale out. Any false claim fails.")

assert len(items) == 72, len(items)
ids = [it["id"] for it in items]
assert len(set(ids)) == len(ids)
with OUT.open("w") as f:
    for it in items:
        f.write(json.dumps(it, ensure_ascii=False) + "\n")
cats = {}
for it in items:
    cats[it["category"]] = cats.get(it["category"], 0) + 1
print(len(items), "items", cats, "probes:", sum(1 for it in items if it.get("hallucination_probe")))
