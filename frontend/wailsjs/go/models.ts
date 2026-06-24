export namespace app {
	
	export class AboutInfo {
	    appName: string;
	    appVersion: string;
	    appAuthor: string;
	    appURL: string;
	    bilibiliURL: string;
	    appDesc: string;
	    sponsorURL: string;
	
	    static createFrom(source: any = {}) {
	        return new AboutInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.appName = source["appName"];
	        this.appVersion = source["appVersion"];
	        this.appAuthor = source["appAuthor"];
	        this.appURL = source["appURL"];
	        this.bilibiliURL = source["bilibiliURL"];
	        this.appDesc = source["appDesc"];
	        this.sponsorURL = source["sponsorURL"];
	    }
	}
	export class ConfigInfo {
	    community: string;
	    ip: string;
	    key_masked: string;
	    key_set: boolean;
	    supernode: string;
	
	    static createFrom(source: any = {}) {
	        return new ConfigInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.community = source["community"];
	        this.ip = source["ip"];
	        this.key_masked = source["key_masked"];
	        this.key_set = source["key_set"];
	        this.supernode = source["supernode"];
	    }
	}
	export class ConnectionDetails {
	    connected: boolean;
	    virtualIP: string;
	    nodeName: string;
	    status: string;
	    sponsorURL: string;
	
	    static createFrom(source: any = {}) {
	        return new ConnectionDetails(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.virtualIP = source["virtualIP"];
	        this.nodeName = source["nodeName"];
	        this.status = source["status"];
	        this.sponsorURL = source["sponsorURL"];
	    }
	}
	export class NodeInfo {
	    name: string;
	    address: string;
	
	    static createFrom(source: any = {}) {
	        return new NodeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.address = source["address"];
	    }
	}
	export class NodeLatencyInfo {
	    name: string;
	    address: string;
	    latency: number;
	
	    static createFrom(source: any = {}) {
	        return new NodeLatencyInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.address = source["address"];
	        this.latency = source["latency"];
	    }
	}
	export class PluginDebugInfo {
	    state: string;
	    isHost: boolean;
	    hostIP: string;
	    myIP: string;
	    community: string;
	    connected: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PluginDebugInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.isHost = source["isHost"];
	        this.hostIP = source["hostIP"];
	        this.myIP = source["myIP"];
	        this.community = source["community"];
	        this.connected = source["connected"];
	    }
	}

}

export namespace plugin {
	
	export class Action {
	    id: string;
	    label: string;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Action(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.enabled = source["enabled"];
	    }
	}
	export class Meta {
	    id: string;
	    name: string;
	    description: string;
	    game: string;
	    runtime_dir: string;
	    requires_admin: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Meta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.game = source["game"];
	        this.runtime_dir = source["runtime_dir"];
	        this.requires_admin = source["requires_admin"];
	    }
	}
	export class Status {
	    state: string;
	    message: string;
	    details: Record<string, string>;
	    actions: Action[];
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.message = source["message"];
	        this.details = source["details"];
	        this.actions = this.convertValues(source["actions"], Action);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

