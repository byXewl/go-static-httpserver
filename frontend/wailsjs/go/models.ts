export namespace main {
	
	export class IPInfo {
	    ip: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new IPInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ip = source["ip"];
	        this.name = source["name"];
	    }
	}

}

