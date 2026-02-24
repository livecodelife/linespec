"use strict";
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
exports.LineSpecError = void 0;
exports.validate = validate;
const fs = __importStar(require("fs"));
const path = __importStar(require("path"));
const lexer_1 = require("./lexer");
Object.defineProperty(exports, "LineSpecError", { enumerable: true, get: function () { return lexer_1.LineSpecError; } });
function validate(spec, baseDir) {
    for (const expect of spec.expects) {
        if ((expect.channel === 'HTTP' || expect.channel === 'WRITE_POSTGRESQL') &&
            !expect.returnsFile) {
            throw new lexer_1.LineSpecError(`RETURNS is required for EXPECT ${expect.channel}`);
        }
    }
    for (const expect of spec.expects) {
        if ((expect.channel === 'WRITE_POSTGRESQL' || expect.channel === 'EVENT') &&
            !expect.withFile) {
            throw new lexer_1.LineSpecError(`WITH is required for EXPECT ${expect.channel}`);
        }
    }
    if (spec.respond.statusCode < 100 || spec.respond.statusCode > 599) {
        throw new lexer_1.LineSpecError(`Invalid HTTP status code: ${spec.respond.statusCode}`);
    }
    const seenKeys = new Set();
    for (const expect of spec.expects) {
        let key;
        if (expect.channel === 'HTTP') {
            key = `HTTP:${expect.method}:${expect.url}`;
        }
        else if (expect.channel === 'READ_MYSQL' || expect.channel === 'WRITE_MYSQL' || expect.channel === 'WRITE_POSTGRESQL') {
            // Include SQL query in key if USING_SQL is present to allow multiple operations on same table
            const sql = expect.sql;
            if (sql) {
                key = `${expect.channel}:${expect.table}:${sql}`;
            }
            else {
                key = `${expect.channel}:${expect.table}`;
            }
        }
        else {
            key = `EVENT:${expect.topic}`;
        }
        if (seenKeys.has(key)) {
            throw new lexer_1.LineSpecError(`Duplicate EXPECT: ${key}`);
        }
        seenKeys.add(key);
    }
    const fileRefs = [];
    if (spec.receive.withFile) {
        fileRefs.push(spec.receive.withFile);
    }
    for (const expect of spec.expects) {
        if (expect.withFile) {
            fileRefs.push(expect.withFile);
        }
        if (expect.returnsFile) {
            fileRefs.push(expect.returnsFile);
        }
    }
    if (spec.respond.withFile) {
        fileRefs.push(spec.respond.withFile);
    }
    for (const file of fileRefs) {
        const resolved = path.resolve(baseDir, file);
        if (!fs.existsSync(resolved)) {
            throw new lexer_1.LineSpecError(`Payload file not found: ${file}`);
        }
    }
    for (const expect of spec.expects) {
        if (expect.sql !== undefined && expect.sql.trim() === '') {
            throw new lexer_1.LineSpecError('USING_SQL block is empty');
        }
    }
}
