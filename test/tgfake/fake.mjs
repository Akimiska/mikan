// A fake Telegram Bot API for developing the bot's admin page locally:
//   node test/tgfake/fake.mjs            (listens on 127.0.0.1:18081)
//   MIKAN_TG_API=http://127.0.0.1:18081 bin/mikan serve
// Any token that looks like one works; messages the bot sends are printed.
// POST /push {"from": 555, "text": "/start …"} queues a message from a user.
import { createServer } from "node:http";

const port = Number(process.env.PORT || 18081);
let messageID = 100;
let updateID = 1;
const queue = [];
const sent = [];

createServer((req, res) => {
  let body = "";
  req.on("data", (c) => (body += c));
  req.on("end", () => {
    if (req.url === "/push") {
      const { from, text } = JSON.parse(body || "{}");
      queue.push({ update_id: updateID++, message: { message_id: ++messageID, from: { id: from, first_name: "Dev", username: "dev" }, chat: { id: from, type: "private" }, text } });
      res.end("queued");
      return;
    }
    if (req.url === "/sent") {
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify(sent.slice(-10)));
      return;
    }
    const method = req.url.split("/").pop();
    const ok = (result) => {
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify({ ok: true, result }));
    };
    switch (method) {
      case "getMe":
        return ok({ id: 123456789, is_bot: true, first_name: "Mikan VPN", username: "mikan_dev_bot" });
      case "getUpdates": {
        // Long polling: whatever was pushed, or nothing after a while.
        const started = Date.now();
        const wait = () => {
          if (queue.length || Date.now() - started > 5000) return ok(queue.splice(0));
          setTimeout(wait, 100);
        };
        return wait();
      }
      case "sendMessage":
        console.log("sendMessage", body);
        sent.push(JSON.parse(body));
        return ok({ message_id: ++messageID, chat: { id: 0, type: "private" } });
      default:
        return ok(true);
    }
  });
}).listen(port, "127.0.0.1", () => console.log(`fake Telegram API on http://127.0.0.1:${port}`));
