import { readFileSync, writeFileSync } from "node:fs";
import path from "node:path";

interface Options {
  input: string;
  output?: string;
  category?: string;
  format: "json" | "text";
}
interface Transaction {
  date: string;
  category: string;
  merchant: string;
  cents: number;
  tags: string[];
}
interface CategorySummary {
  category: string;
  count: number;
  cents: number;
  merchants: string[];
}
interface LedgerReport {
  source: string;
  accepted: number;
  rejected: number;
  cents: number;
  categories: CategorySummary[];
  months: Record<string, number>;
  errors: string[];
}

class InputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "InputError";
  }
}

function parseOptions(args: string[]): Options {
  const options: Options = { input: "", format: "text" };
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (arg === "--json") {
      options.format = "json";
    } else if (arg === "--output" || arg === "--category") {
      const value: string | undefined = args[++i];
      if (value === undefined || value.startsWith("--")) {
        throw new InputError(`Missing value after ${arg}`);
      }
      if (arg === "--output") options.output = value;
      else options.category = value;
    } else if (arg.startsWith("--")) {
      throw new InputError(`Unknown option ${arg}`);
    } else if (options.input === "") {
      options.input = arg;
    } else {
      throw new InputError(`Unexpected argument ${arg}`);
    }
  }
  if (options.input === "") {
    throw new InputError("Usage: ledger file.csv [--json] [--category name] [--output file]");
  }
  return options;
}

function parseCsvRow(line: string): string[] {
  const columns: string[] = [];
  let value = "";
  let quoted = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (c === '"') {
      if (quoted && line[i + 1] === '"') {
        value += '"';
        i++;
      } else {
        quoted = !quoted;
      }
    } else if (c === "," && !quoted) {
      columns.push(value.trim());
      value = "";
    } else {
      value += c;
    }
  }
  if (quoted) throw new InputError("Unclosed quoted field");
  columns.push(value.trim());
  return columns;
}

function parseCents(text: string): number {
  const match = /^(-?)(\d+)(?:\.(\d{1,2}))?$/.exec(text);
  if (match === null) throw new InputError(`Invalid amount ${text}`);
  const fraction = (match[3] ?? "").padEnd(2, "0");
  const cents = Number(match[2]) * 100 + Number(fraction);
  if (!Number.isSafeInteger(cents)) throw new InputError("Amount is too large");
  return match[1] === "-" ? -cents : cents;
}

function parseTransaction(columns: string[]): Transaction {
  if (columns.length !== 5) throw new InputError("Expected five columns");
  const [date, category, merchant, amount, tagText] = columns;
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) throw new InputError(`Invalid date ${date}`);
  if (!Number.isFinite(Date.parse(`${date}T00:00:00Z`))) {
    throw new InputError(`Invalid date ${date}`);
  }
  if (category === "" || merchant === "") throw new InputError("Category and merchant are required");
  return {
    date,
    category: category.normalize("NFC").toLowerCase(),
    merchant: merchant.normalize("NFC"),
    cents: parseCents(amount),
    tags: tagText.split(";").map((tag) => tag.trim()).filter((tag) => tag !== ""),
  };
}

class Ledger {
  private transactions: Transaction[] = [];
  private errors: string[] = [];

  addLine(line: string, lineNumber: number): void {
    if (line.trim() === "") return;
    try {
      this.transactions.push(parseTransaction(parseCsvRow(line)));
    } catch (e) {
      if (!(e instanceof Error)) throw e;
      this.errors.push(`line ${lineNumber}: ${e.message}`);
    }
  }

  report(source: string, category?: string): LedgerReport {
    const selected = this.transactions.filter((entry) => category === undefined || entry.category === category);
    const grouped = groupBy(selected, (entry) => entry.category);
    const categories: CategorySummary[] = [];
    for (const [name, entries] of grouped) {
      categories.push({
        category: name,
        count: entries.length,
        cents: entries.reduce((sum, entry) => sum + entry.cents, 0),
        merchants: [...new Set(entries.map((entry) => entry.merchant))].sort(),
      });
    }
    categories.sort((a, b) => b.cents - a.cents || a.category.localeCompare(b.category));
    const months: Record<string, number> = {};
    for (const entry of selected) {
      const month = entry.date.slice(0, 7);
      months[month] = (months[month] ?? 0) + entry.cents;
    }
    return {
      source,
      accepted: selected.length,
      rejected: this.errors.length,
      cents: selected.reduce((sum, entry) => sum + entry.cents, 0),
      categories,
      months,
      errors: [...this.errors],
    };
  }
}

function groupBy<T>(items: T[], keyOf: (item: T) => string): Map<string, T[]> {
  const groups = new Map<string, T[]>();
  for (const item of items) {
    const key = keyOf(item);
    const group = groups.get(key);
    if (group === undefined) groups.set(key, [item]);
    else group.push(item);
  }
  return groups;
}

function formatMoney(cents: number): string {
  const sign = cents < 0 ? "-" : "";
  const magnitude = Math.abs(cents);
  return `${sign}${Math.floor(magnitude / 100)}.${String(magnitude % 100).padStart(2, "0")}`;
}

function formatReport(report: LedgerReport): string {
  const lines = [
    `Ledger: ${report.source}`,
    `Accepted ${report.accepted}; rejected ${report.rejected}`,
    `Total ${formatMoney(report.cents)}`,
    "",
    "Category          Count       Total",
  ];
  for (const category of report.categories) {
    lines.push(`${category.category.padEnd(18)}${String(category.count).padStart(5)}  ${formatMoney(category.cents).padStart(10)}`);
    lines.push(`  ${category.merchants.join(", ")}`);
  }
  if (report.errors.length > 0) {
    lines.push("", "Rejected records:", ...report.errors);
  }
  return lines.join("\n");
}

function loadLedger(file: string): Ledger {
  const ledger = new Ledger();
  const lines = readFileSync(file, "utf8").split(/\r?\n/);
  if (lines[0] !== "date,category,merchant,amount,tags") {
    throw new InputError("Expected header date,category,merchant,amount,tags");
  }
  for (let i = 1; i < lines.length; i++) ledger.addLine(lines[i], i + 1);
  return ledger;
}

function main(): void {
  try {
    const options = parseOptions(process.argv.slice(2));
    const ledger = loadLedger(options.input);
    const report = ledger.report(path.basename(options.input), options.category?.toLowerCase());
    const text = options.format === "json" ? JSON.stringify(report, null, 2) : formatReport(report);
    if (options.output !== undefined) writeFileSync(options.output, text + "\n");
    else console.log(text);
    if (report.rejected > 0) process.exitCode = 1;
  } catch (e) {
    console.error(e instanceof Error ? e.message : String(e));
    process.exitCode = 2;
  }
}
main();
