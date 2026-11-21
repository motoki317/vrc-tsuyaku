# Glossary

Speech recognition does not know the names that your friends use. It writes the avatar name 桔梗 as 帰郷, and "Manuka" as "Manyuka". Settings → Glossary holds entries for such words. The glossary stays on your PC.

An entry pairs a word in one language with the word that you want for it in another:

| Language and word | Kana reading | | Language and word |
|---|---|---|---|
| Japanese: 桔梗 | ききょう | ↔ | English: Kikyo |
| Japanese: フルトラ | | ↔ | English: full-body tracking |

An entry applies when its two languages are the two languages of "My voice" or of "Others' voices", in either order. A Japanese speaker's 桔梗 becomes "Kikyo" in the translation, and an English speaker's "Kikyo" becomes 桔梗.

For each sentence, the app does three things with an entry's word:

1. Recognition favors the word over words that sound like it: "Manuka" over "Manyuka".
2. The app writes the word in the transcript the way the entry writes it: "manuka" becomes "Manuka".
3. The translation uses the entry's other word.

Two languages get more help:

- Japanese: fill in "Kana reading" when the word has kanji or Latin letters. Recognition often writes an unknown name in kana, and it also favors the entry's reading. The app then replaces the reading with the word: ききょう becomes 桔梗.
- English: the app also replaces a word that sounds like the entry's word. With an entry for "Selestia", "Celestia" becomes "Selestia".

In a test with synthetic voices, the right name came out in about 2 of 3 sentences, and in about 1 of 3 without the glossary.

An entry applies wherever its word, its reading or its sound occurs. With an entry that pairs 真冬 with "Mafuyu", a midwinter morning (真冬の朝) becomes "Mafuyu's morning". If an entry changes sentences that it must not change, remove it or clear its reading.

Before you go back to VRC Tsuyaku 0.2.0 or older, remove every entry. Those versions show a settings error while the glossary has entries.
